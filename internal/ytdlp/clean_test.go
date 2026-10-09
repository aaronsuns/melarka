package ytdlp

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanTitle(t *testing.T) {
	cases := []struct {
		title, channel  string
		wantTitle, want string
	}{
		{"邓丽君 - 甜蜜蜜【MV】", "Teresa Teng Official", "甜蜜蜜", "邓丽君"},
		{"Alan Walker - Faded (Official Music Video)", "Alan Walker", "Faded", "Alan Walker"},
		{"周杰伦《晴天》高音质 [HD]", "JayChou", "晴天", "周杰伦"},
		{"River Flows In You", "Yiruma - Topic", "River Flows In You", "Yiruma"},
		{"Song (feat. B) [Lyrics]", "Chan", "Song (feat. B)", "Chan"},
		{"【4K】優しい癒しBGM", "BGM Channel", "優しい癒しBGM", "BGM Channel"},
		// The dash split requires whitespace around the dash, so a bare
		// artist-name hyphen (no surrounding spaces) must not be mistaken
		// for the "artist - title" separator.
		{"AC-DC Thunderstruck", "AC/DC - Topic", "AC-DC Thunderstruck", "AC/DC"},
		{"Jay-Z - Song", "SomeChannel", "Song", "Jay-Z"},
		// Noise-word matching is whole-word for ASCII tokens: "live" must
		// not fire on the "live" hiding inside "Oliver".
		{"Song (Oliver Remix)", "SomeChannel", "Song (Oliver Remix)", "SomeChannel"},
		{"Song (feat. Olivia X)", "SomeChannel2", "Song (feat. Olivia X)", "SomeChannel2"},
		// Filler around the singer's name is not part of it.
		{"布仁巴雅尔原唱的歌曲《天边》，深沉悠远，具有浓浓的草原风味！", "草原音乐", "天边", "布仁巴雅尔"},
		{"【纯享】#张信哲 深情演绎《信仰》张信哲的嗓音太绝了 | 我是歌手4", "湖南卫视芒果TV官方频道", "信仰", "张信哲"},
		{"邓丽君演唱《甜蜜蜜》", "Chan", "甜蜜蜜", "邓丽君"},
		{"原唱：刀郎《西海情歌》", "Chan", "西海情歌", "刀郎"},
		{"周深翻唱《大鱼》", "Chan", "大鱼", "周深"},
		{"River Flows In You", "Yiruma翻唱 - Topic", "River Flows In You", "Yiruma"},
		{"刀郎的《西海情歌》", "Chan", "西海情歌", "刀郎"},
		{"原唱 的《歌》", "Chan", "歌", "原唱"},
		{"Song", "我的", "Song", "我的"},
		{"Song", "小鱼的", "Song", "小鱼的"},
		{"Song", "-M-", "Song", "-M-"},
		{"Song", "Sha-", "Song", "Sha-"},
		{"Song", "#1 Dads", "Song", "#1 Dads"},
	}
	for _, c := range cases {
		gotTitle, gotArtist := CleanTitle(c.title, c.channel)
		if gotTitle != c.wantTitle || gotArtist != c.want {
			t.Errorf("CleanTitle(%q, %q) = (%q, %q), want (%q, %q)",
				c.title, c.channel, gotTitle, gotArtist, c.wantTitle, c.want)
		}
	}
}

func TestCleanArtist(t *testing.T) {
	cases := map[string]string{
		"布仁巴雅尔原唱的歌曲":    "布仁巴雅尔",
		"布仁巴雅尔原唱歌曲":     "布仁巴雅尔",
		"布仁巴雅尔的歌曲":      "布仁巴雅尔",
		"张信哲 深情演绎":      "张信哲",
		"【纯享】#张信哲 深情演绎": "张信哲",
		"毛阿敏 演绎":        "毛阿敏",
		"献唱 韩红":         "韩红",
		"小鱼演唱的":         "小鱼",
		// A bare trailing 的 is part of a channel/tag name; only a title split drops it.
		"刀郎的":  "刀郎的",
		"我的":   "我的",
		"小鱼的":  "小鱼的",
		"原唱 的": "原唱 的", // never just 的
		// ASCII punctuation and a non-Han hashtag can be part of a real name.
		"-M-":         "-M-",
		"Sha-":        "Sha-",
		"#1 Dads":     "#1 Dads",
		"AC/DC":       "AC/DC",
		"邓丽君":         "邓丽君",
		"Alan Walker": "Alan Walker",
		// Only affixes: a name with the word inside it stays whole.
		"李演唱团":  "李演唱团",
		"原唱者乐队": "原唱者乐队",
		// Nothing but filler: keep what there was rather than an empty artist.
		"原唱": "原唱",
		"":   "",
	}
	for in, want := range cases {
		if got := CleanArtist(in); got != want {
			t.Errorf("CleanArtist(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeName(t *testing.T) {
	if got := SafeName("a/b:c*?"); got != "a_b_c__" {
		t.Errorf("SafeName(%q) = %q, want %q", "a/b:c*?", got, "a_b_c__")
	}
	if got := SafeName("..."); got != "untitled" {
		t.Errorf("SafeName(%q) = %q, want %q", "...", got, "untitled")
	}
	if got := SafeName("   "); got != "untitled" {
		t.Errorf("SafeName(%q) = %q, want %q", "   ", got, "untitled")
	}

	long := strings.Repeat("音", 100) // 3 bytes/rune, 300 bytes total
	if len(long) != 300 {
		t.Fatalf("test setup: expected 300 bytes, got %d", len(long))
	}
	got := SafeName(long)
	if len(got) > 120 {
		t.Errorf("SafeName(300-byte name) = %d bytes, want <= 120", len(got))
	}
	if !utf8.ValidString(got) {
		t.Errorf("SafeName(300-byte name) produced invalid UTF-8: %q", got)
	}
}

func TestSafeName_InvalidUTF8(t *testing.T) {
	got := SafeName("x\xff音乐")
	if !utf8.ValidString(got) {
		t.Fatalf("SafeName produced invalid UTF-8: %q", got)
	}
	if got != "x音乐" {
		t.Errorf("SafeName(%q) = %q, want %q (the lone invalid byte dropped)", "x\xff音乐", got, "x音乐")
	}
}

// TestSafeName_RetrimsAfterTruncate constructs a name where the naive
// 120-byte cut lands inside a multi-byte rune just past a literal ". ": the
// rune-boundary back-off alone would leave that ". " dangling at the new
// end, so SafeName must re-trim dots/spaces (and re-check emptiness) after
// truncating, not just before.
func TestSafeName_RetrimsAfterTruncate(t *testing.T) {
	prefix := strings.Repeat("音", 39) // 117 bytes, so prefix+". " ends exactly at byte 119
	in := prefix + ". " + strings.Repeat("字", 50)

	got := SafeName(in)
	if !utf8.ValidString(got) {
		t.Fatalf("SafeName produced invalid UTF-8: %q", got)
	}
	if len(got) > 120 {
		t.Errorf("SafeName = %d bytes, want <= 120", len(got))
	}
	if strings.HasSuffix(got, ".") || strings.HasSuffix(got, " ") {
		t.Errorf("SafeName(%q) = %q, left a trailing dot/space after truncation", in, got)
	}
	if got != prefix {
		t.Errorf("SafeName(%q) = %q, want %q", in, got, prefix)
	}
}

func TestValidURL(t *testing.T) {
	accept := []string{
		"https://www.youtube.com/watch?v=abc",
		"http://www.youtube.com/watch?v=abc", // http is accepted (and normalised, see below)
		"https://youtu.be/abc",
		"https://music.youtube.com/playlist?list=PL1",
	}
	for _, u := range accept {
		if _, err := ValidURL(u); err != nil {
			t.Errorf("ValidURL(%q) = %v, want nil error", u, err)
		}
	}

	reject := []string{
		"--exec=x",
		"file:///etc/passwd",
		"https://evil.com/watch?v=abc",
		"ftp://youtube.com/x",
		"",
		"https://www.youtube.com:8443/watch?v=abc", // explicit port rejected outright
	}
	for _, u := range reject {
		if _, err := ValidURL(u); err != ErrBadURL {
			t.Errorf("ValidURL(%q) = %v, want ErrBadURL", u, err)
		}
	}
}

func TestValidURL_NormalisesSchemeAndDropsUserinfo(t *testing.T) {
	got, err := ValidURL("http://user:pass@www.youtube.com/watch?v=abc")
	if err != nil {
		t.Fatalf("ValidURL: %v", err)
	}
	want := "https://www.youtube.com/watch?v=abc"
	if got != want {
		t.Errorf("ValidURL(http with userinfo) = %q, want %q", got, want)
	}
}

func TestCleanQuery(t *testing.T) {
	if _, err := CleanQuery(""); err != ErrBadQuery {
		t.Errorf("CleanQuery(empty) = %v, want ErrBadQuery", err)
	}
	if _, err := CleanQuery("   "); err != ErrBadQuery {
		t.Errorf("CleanQuery(whitespace) = %v, want ErrBadQuery", err)
	}
	if _, err := CleanQuery(strings.Repeat("a", 101)); err != ErrBadQuery {
		t.Errorf("CleanQuery(101 runes) = %v, want ErrBadQuery", err)
	}
	if _, err := CleanQuery(strings.Repeat("a", 100)); err != nil {
		t.Errorf("CleanQuery(100 runes) = %v, want nil", err)
	}
	got, err := CleanQuery("-rf")
	if err != nil || got != "-rf" {
		t.Errorf("CleanQuery(-rf) = (%q, %v), want (\"-rf\", nil)", got, err)
	}
}

// "%" would be read by yt-dlp as the start of an output-template field
// ("%(ext)s") when the name becomes part of -o.
func TestSafeName_Percent(t *testing.T) {
	if got := SafeName("100% 爱(title)s"); got != "100_ 爱(title)s" {
		t.Errorf("SafeName = %q, want %q", got, "100_ 爱(title)s")
	}
}

// CleanTitleV2 is frozen: the missing-lyrics list compares overrides against it.
func TestCleanTitleV2Frozen(t *testing.T) {
	const raw = "盧冠廷 莫文蔚【一生所愛 Love In A Life Time】電影「大话西游」插曲【创作Creative MV Lyrics】「一生所爱」原创remix"
	if title, artist := CleanTitleV2(raw, "Chan"); title != "大话西游" || artist != "盧冠廷 莫文蔚【一生所愛 Love In A Life Time】電影" {
		t.Errorf("CleanTitleV2 = (%q, %q)", title, artist)
	}
	if title, artist := CleanTitleV2("布仁巴雅尔原唱的歌曲《天边》", "Chan"); title != "天边" || artist != "布仁巴雅尔" {
		t.Errorf("CleanTitleV2 = (%q, %q)", title, artist)
	}
}

// The persisted cleaner is exactly CleanTitleV2 (Ruling Q28): film/drama
// context, A【B】 and leading-quote shapes are search hints only.
func TestCleanTitleIsV2(t *testing.T) {
	for _, c := range [][2]string{
		{"儿歌【小星星】", "宝宝巴士"},
		{"第1集【小猪佩奇】", "小猪佩奇中文官方"},
		{"钢琴曲【梦中的婚礼】", "Piano"},
		{"古筝【高山流水】纯音乐", "Chan"},
		{"轻音乐【雨的印记】", "Chan"},
		{"抖音热歌【学猫叫】", "Chan"},
		{"2023最火歌曲【孤勇者】", "Chan"},
		{"KTV【后来】伴奏", "Chan"},
		{"Lyrics【后来】", "Chan"},
		{"DJ【野狼disco】", "Chan"},
		{"经典老歌合集【甜蜜蜜】", "Chan"},
		{"BGM【千本桜】", "Chan"},
		{"盧冠廷 莫文蔚【一生所愛 Love In A Life Time】電影「大话西游」插曲【创作Creative MV Lyrics】「一生所爱隐约在白云」原创remix", "華音殿Music Channel"},
		{"电影《英雄本色》主题曲【粤语】当年情", "Chan"},
		{"电影《英雄本色》主题曲【张国荣】当年情", "Chan"},
		{"电影《英雄本色》主题曲【经典老歌】当年情", "Chan"},
		{"动画片《葫芦娃》主题曲 儿歌合集 30分钟", "Chan"},
		{"《新白娘子传奇》插曲 合集", "Chan"},
		{"电影《唐伯虎点秋香》插曲【中文字幕】", "Chan"},
		{"电影《大话西游》插曲【一生所爱】卢冠廷", "Chan"},
		{"电视剧《还珠格格》插曲【有一个姑娘】赵薇", "Chan"},
		{"电视剧《还珠格格》主题曲 动力火车《当》", "Chan"},
		{"電影「霸王別姬」主題曲 張國榮 當愛已成往事", "Chan"},
		{"刘欢《好汉歌》电视剧主题曲", "Chan"},
		{"甄嬛传 主题曲【红颜劫】", "Chan"},
		{"甄嬛传 主题曲【红颜劫】姚贝娜", "Chan"},
		{"张学友&汤宝如【相思风雨中】Live", "Chan"},
		{"刘德华、陈慧琳【我和你】", "Chan"},
		{"和平【一首歌】", "Chan"},
		{"张信哲【纯享】《信仰》", "Chan"},
		{"Alan Walker【Faded】", "Chan"},
		{"粤语【千千阙歌】", "Chan"},
		{"怀旧金曲【千千阙歌】陈慧娴", "Chan"},
		{"官方【后来】", "Chan"},
		{"无损【后来】", "Chan"},
		{"后来【刘若英】", "Chan"},
		{"孤勇者【陈奕迅】", "Chan"},
		{"月亮代表我的心【邓丽君】经典老歌", "Chan"},
		{"千千阙歌【粤语】", "Chan"},
		// "Singer《Song》…主题曲": the quote after a singer is the song (named like its drama).
		{"刘欢《好汉歌》电视剧主题曲", "Chan"},
		{"毛阿敏《渴望》主题曲", "Chan"},
		{"周杰伦 《不能说的秘密》电影主题曲", "Chan"},
	} {
		wantT, wantA := CleanTitleV2(c[0], c[1])
		if gotT, gotA := CleanTitle(c[0], c[1]); gotT != wantT || gotA != wantA {
			t.Errorf("CleanTitle(%q) = (%q, %q), want V2's (%q, %q)", c[0], gotT, gotA, wantT, wantA)
		}
	}
}
