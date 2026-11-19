package ytdlp

import "testing"

const bv = "BV1GJ411x7h7" // a real-shaped Bilibili id (BV + 10)

func TestIsAnyVideoID(t *testing.T) {
	cases := map[string]bool{
		vid:             true,  // YouTube
		bv:              true,  // Bilibili
		"BVabcdefghi":   true,  // 11 chars starting BV: a YouTube id
		"BV1GJ411x7h7x": false, // 13 chars
		"XX1GJ411x7h7":  false, // 12 chars, not BV
		"BV1GJ411x7-7":  false, // Bilibili ids have no - or _
		"":              false,
	}
	for id, want := range cases {
		if got := IsAnyVideoID(id); got != want {
			t.Errorf("IsAnyVideoID(%q) = %v, want %v", id, got, want)
		}
	}
	if IsVideoID(bv) {
		t.Errorf("IsVideoID must stay YouTube-only, accepted %q", bv)
	}
}

func TestSourceOf(t *testing.T) {
	cases := []struct {
		id   string
		want Source
		ok   bool
	}{
		{vid, SourceYouTube, true},
		{"BVabcdefghi", SourceYouTube, true},
		{bv, SourceBilibili, true},
		{"nope", "", false},
	}
	for _, c := range cases {
		got, ok := SourceOf(c.id)
		if got != c.want || ok != c.ok {
			t.Errorf("SourceOf(%q) = %q,%v want %q,%v", c.id, got, ok, c.want, c.ok)
		}
	}
}

func TestSourceOfURL(t *testing.T) {
	cases := []struct {
		url  string
		want Source
		ok   bool
	}{
		{"https://www.youtube.com/watch?v=" + vid, SourceYouTube, true},
		{"https://youtu.be/" + vid, SourceYouTube, true},
		{"https://www.bilibili.com/video/" + bv, SourceBilibili, true},
		{"https://space.bilibili.com/123/favlist?fid=456", SourceBilibili, true},
		{"https://bilibili.com.evil.test/video/" + bv, "", false},
		{"https://b23.tv/abc", "", false},
	}
	for _, c := range cases {
		got, ok := SourceOfURL(c.url)
		if got != c.want || ok != c.ok {
			t.Errorf("SourceOfURL(%q) = %q,%v want %q,%v", c.url, got, ok, c.want, c.ok)
		}
	}
}

func TestWatchURLBilibili(t *testing.T) {
	if got := WatchURL(bv); got != "https://www.bilibili.com/video/"+bv {
		t.Errorf("WatchURL(bv) = %q", got)
	}
	if got := WatchURL(vid); got != "https://www.youtube.com/watch?v="+vid {
		t.Errorf("WatchURL(vid) = %q", got)
	}
}

func TestThumbnailURLBilibiliEmpty(t *testing.T) {
	if got := ThumbnailURL(bv); got != "" {
		t.Errorf("ThumbnailURL(bv) = %q, want empty", got)
	}
	if ThumbnailURL(vid) == "" {
		t.Error("ThumbnailURL(vid) must stay derived")
	}
}

func TestVideoIDFromURLBilibili(t *testing.T) {
	cases := map[string]string{
		"https://www.bilibili.com/video/" + bv:                     bv,
		"https://www.bilibili.com/video/" + bv + "/":               bv,
		"https://www.bilibili.com/video/" + bv + "?p=2":            bv,
		"https://www.bilibili.com/video/" + bv + "/?spm_id_from=x": bv,
		"https://m.bilibili.com/video/" + bv:                       bv,
		"https://bilibili.com/video/" + bv:                         bv,
		"https://www.bilibili.com/video/av170001":                  "",
		"https://space.bilibili.com/123/favlist?fid=456":           "",
		"https://www.bilibili.com/video/" + bv + "x":               "",
	}
	for u, want := range cases {
		if got := VideoIDFromURL(u); got != want {
			t.Errorf("VideoIDFromURL(%q) = %q, want %q", u, got, want)
		}
	}
}

func TestValidURLBilibiliHosts(t *testing.T) {
	ok := []string{
		"https://bilibili.com/video/" + bv,
		"https://www.bilibili.com/video/" + bv,
		"http://m.bilibili.com/video/" + bv,
		"https://space.bilibili.com/123/favlist?fid=456",
	}
	for _, u := range ok {
		if _, err := ValidURL(u); err != nil {
			t.Errorf("ValidURL(%q) = %v, want ok", u, err)
		}
	}
	bad := []string{
		"https://bilibili.com.evil.test/video/" + bv,
		"https://www.bilibili.com:8080/video/" + bv,
		"-https://www.bilibili.com/video/" + bv,
		"ftp://www.bilibili.com/video/" + bv,
		"https://b23.tv/abc",
	}
	for _, u := range bad {
		if _, err := ValidURL(u); err == nil {
			t.Errorf("ValidURL(%q) accepted", u)
		}
	}
	got, err := ValidURL("https://user:pw" + "@" + "www.bilibili.com/video/" + bv)
	if err != nil || got != "https://www.bilibili.com/video/"+bv {
		t.Errorf("userinfo not stripped: %q %v", got, err)
	}
}

func TestIsBilibiliListURL(t *testing.T) {
	cases := map[string]bool{
		"https://space.bilibili.com/123/favlist?fid=456":                  true,
		"https://space.bilibili.com/123/channel/collectiondetail?sid=789": true,
		"https://space.bilibili.com/123/channel/seriesdetail?sid=789":     true,
		"https://space.bilibili.com/123/video":                            true,
		"https://www.bilibili.com/medialist/detail/ml456":                 true,
		"https://www.bilibili.com/list/123?sid=789":                       true,
		"https://www.bilibili.com/video/" + bv:                            false,
		"https://www.youtube.com/playlist?list=PLabcdefghijklmnop":        false,
		"https://evil.test/medialist/detail/ml456":                        false,
	}
	for u, want := range cases {
		if got := IsBilibiliListURL(u); got != want {
			t.Errorf("IsBilibiliListURL(%q) = %v, want %v", u, got, want)
		}
	}
}

func TestBilibiliCover(t *testing.T) {
	cases := map[string]string{
		"http://i0.hdslb.com/bfs/archive/a.jpg":  "https://i0.hdslb.com/bfs/archive/a.jpg",
		"https://i2.hdslb.com/bfs/archive/b.png": "https://i2.hdslb.com/bfs/archive/b.png",
		"https://hdslb.com.evil.test/a.jpg":      "",
		"https://i0.hdslb.com:81/a.jpg":          "",
		"javascript:alert(1)":                    "",
		"":                                       "",
	}
	for in, want := range cases {
		if got := BilibiliCover(in); got != want {
			t.Errorf("BilibiliCover(%q) = %q, want %q", in, got, want)
		}
	}
}
