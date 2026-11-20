package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSearchSourceBilibiliArgv(t *testing.T) {
	fr := &fakeRunner{output: fixture(t, "bilisearch.jsonl")}
	cl := &Client{Runner: fr}
	if _, _, err := cl.SearchSource(context.Background(), SourceBilibili, " 晴天 ", 10); err != nil {
		t.Fatal(err)
	}
	argv := strings.Join(fr.gotArgs, " ")
	if !slices.Contains(fr.gotArgs, "bilisearch10:晴天") {
		t.Errorf("want bilisearch10:晴天 in %v", fr.gotArgs)
	}
	if slices.Contains(fr.gotArgs, "--flat-playlist") || strings.Contains(argv, "ytsearch") {
		t.Errorf("Bilibili search must be a full, non-YouTube lookup: %v", fr.gotArgs)
	}
	for _, f := range []string{"--no-playlist", "--skip-download", "--ignore-no-formats-error", "--ignore-errors"} {
		if !slices.Contains(fr.gotArgs, f) {
			t.Errorf("missing %s in %v", f, fr.gotArgs)
		}
	}
}

func TestSearchSourceBilibiliClamp(t *testing.T) {
	fr := &fakeRunner{output: nil}
	cl := &Client{Runner: fr}
	if _, _, err := cl.SearchSource(context.Background(), SourceBilibili, "x", 50); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fr.gotArgs, fmt.Sprintf("bilisearch%d:x", MaxBilibiliResults)) {
		t.Errorf("n not clamped to %d: %v", MaxBilibiliResults, fr.gotArgs)
	}
}

func TestSearchSourceBilibiliParses(t *testing.T) {
	cl := &Client{Runner: &fakeRunner{output: fixture(t, "bilisearch.jsonl")}}
	videos, more, err := cl.SearchSource(context.Background(), SourceBilibili, "晴天", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(videos) != 3 || !more {
		t.Fatalf("got %d videos more=%v, want 3 and more", len(videos), more)
	}
	v := videos[0]
	if v.ID != "BV1aaaaaaaa1" || v.Title != "周杰伦 晴天" || v.Channel != "某UP主" || v.DurationS != 269 ||
		v.URL != "https://www.bilibili.com/video/BV1aaaaaaaa1" || v.Thumbnail != "https://i0.hdslb.com/bfs/archive/a.jpg" ||
		v.ChannelID != "" || v.Views != 1000 {
		t.Errorf("video 0 = %+v", v)
	}
	if videos[1].ID != "BV1bbbbbbbb2" { // "_p1" suffix of a multi-part video stripped
		t.Errorf("video 1 id = %q", videos[1].ID)
	}
	if videos[2].Thumbnail != "" {
		t.Errorf("video 2 has no cover, got %q", videos[2].Thumbnail)
	}
}

func TestSearchSourceYouTubeDelegates(t *testing.T) {
	fr := &fakeRunner{output: []byte(`{"_type":"playlist","entries":[]}`)}
	cl := &Client{Runner: fr}
	if _, _, err := cl.SearchSource(context.Background(), SourceYouTube, "x", 10); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fr.gotArgs, "ytsearch10:x") {
		t.Errorf("YouTube search argv %v", fr.gotArgs)
	}
}

func TestResolveListBilibiliFavlist(t *testing.T) {
	fr := &fakeRunner{output: fixture(t, "bilifavlist.jsonl")}
	cl := &Client{Runner: fr}
	l, err := cl.ResolveList(context.Background(), "https://space.bilibili.com/123/favlist?fid=456", 50)
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(fr.gotArgs, "--flat-playlist") {
		t.Errorf("Bilibili lists resolve in full: %v", fr.gotArgs)
	}
	if l.ID != "456" || l.Title != "我的收藏" || l.Count != 2 || len(l.Videos) != 2 || l.Videos[1].Title != "第二首" {
		t.Errorf("list = %+v", l)
	}
}

func TestResolveListBilibiliSingleVideo(t *testing.T) {
	fr := &fakeRunner{output: []byte(`{"id":"BV1aaaaaaaa1","title":"一首歌","uploader":"u","duration":60}` + "\n")}
	cl := &Client{Runner: fr}
	l, err := cl.ResolveList(context.Background(), "https://www.bilibili.com/video/BV1aaaaaaaa1?p=2", 50)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fr.gotArgs, "--no-playlist") || l.ID != "" || len(l.Videos) != 1 {
		t.Errorf("single video: argv %v list %+v", fr.gotArgs, l)
	}
}

func TestVideoInfoBilibili(t *testing.T) {
	fr := &fakeRunner{output: []byte(`{"id":"BV1aaaaaaaa1","title":"t","channel":"","duration":60,"thumbnail":"https://i0.hdslb.com/bfs/archive/a.jpg"}`)}
	cl := &Client{Runner: fr}
	info, err := cl.VideoInfo(context.Background(), "BV1aaaaaaaa1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Thumbnail != "https://i0.hdslb.com/bfs/archive/a.jpg" {
		t.Errorf("thumbnail %q", info.Thumbnail)
	}
	if fr.gotArgs[len(fr.gotArgs)-1] != "https://www.bilibili.com/video/BV1aaaaaaaa1" {
		t.Errorf("argv %v", fr.gotArgs)
	}
}

func TestClassifyBilibili412(t *testing.T) {
	raw := errors.New("yt-dlp: exit status 1: ERROR: [BiliBiliSearch] x: Unable to download JSON metadata: HTTP Error 412: Precondition Failed")
	err := ClassifyBilibili(raw)
	if !errors.Is(err, ErrBilibiliThrottled) {
		t.Fatalf("got %v", err)
	}
	if !PushBack(err) {
		t.Error("a throttled Bilibili call is a push-back")
	}
	if ClassifyBilibili(nil) != nil {
		t.Error("nil stays nil")
	}
	other := errors.New("yt-dlp: exit status 1: ERROR: something else")
	if ClassifyBilibili(other) != other {
		t.Error("other errors pass through unchanged")
	}
}

func TestSearchSourceBilibili412(t *testing.T) {
	defer func(d time.Duration) { BilibiliRetryDelay = d }(BilibiliRetryDelay)
	BilibiliRetryDelay = 0
	fr := &fakeRunner{outputErr: errors.New("yt-dlp: exit status 1: ERROR: HTTP Error 412: Precondition Failed")}
	cl := &Client{Runner: fr}
	if _, _, err := cl.SearchSource(context.Background(), SourceBilibili, "x", 10); !errors.Is(err, ErrBilibiliThrottled) {
		t.Errorf("got %v", err)
	}
	if len(fr.calls) != biliSearchAttempts {
		t.Errorf("tried %d times, want %d", len(fr.calls), biliSearchAttempts)
	}
}

// Bilibili's anti-bot check refuses a share of searches at random: a refused
// search is asked again, and the second answer is the result.
func TestSearchSourceBilibiliRetries412(t *testing.T) {
	defer func(d time.Duration) { BilibiliRetryDelay = d }(BilibiliRetryDelay)
	BilibiliRetryDelay = 0
	good := fixture(t, "bilisearch.jsonl")
	fr := &fakeRunner{}
	fr.outFor = func([]string) ([]byte, error) {
		if len(fr.calls) == 1 {
			return nil, errors.New("yt-dlp: exit status 1: ERROR: HTTP Error 412: Precondition Failed")
		}
		return good, nil
	}
	videos, _, err := (&Client{Runner: fr}).SearchSource(context.Background(), SourceBilibili, "七龙珠", 10)
	if err != nil || len(videos) == 0 || len(fr.calls) != 2 {
		t.Fatalf("got %d videos, %v after %d calls", len(videos), err, len(fr.calls))
	}
}

// Other errors are not retried.
func TestSearchSourceBilibiliNoRetryOnOtherErrors(t *testing.T) {
	fr := &fakeRunner{outputErr: errors.New("yt-dlp: exit status 1: ERROR: something else")}
	if _, _, err := (&Client{Runner: fr}).SearchSource(context.Background(), SourceBilibili, "x", 10); err == nil || len(fr.calls) != 1 {
		t.Fatalf("err %v after %d calls", err, len(fr.calls))
	}
}

// A dead entry in a favourites folder makes yt-dlp exit 1 after printing the
// good ones: they are kept.
func TestResolveListBilibiliKeepsPartialOutput(t *testing.T) {
	fr := &fakeRunner{output: fixture(t, "bilifavlist.jsonl"), outputErr: errors.New("yt-dlp: exit status 1: ERROR: [BiliBili] BV1dead: 啊叻？视频不见了？")}
	l, err := (&Client{Runner: fr}).ResolveList(context.Background(), "https://space.bilibili.com/123/favlist?fid=456", 50)
	if err != nil || len(l.Videos) != 2 {
		t.Fatalf("got %d videos, %v", len(l.Videos), err)
	}
}

func TestResolveListBilibiliCapped(t *testing.T) {
	fr := &fakeRunner{output: fixture(t, "bilifavlist.jsonl")}
	if _, err := (&Client{Runner: fr}).ResolveList(context.Background(), "https://space.bilibili.com/123/favlist?fid=456", 200); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fr.gotArgs, fmt.Sprint(MaxBilibiliList)) {
		t.Fatalf("list not capped at %d: %v", MaxBilibiliList, fr.gotArgs)
	}
}

func TestResolveBilibiliPart2UsesCanonicalURL(t *testing.T) {
	fr := &fakeRunner{output: []byte(`{"id":"BV1aaaaaaaa1","title":"t"}` + "\n")}
	if _, err := (&Client{Runner: fr}).ResolveList(context.Background(), "https://www.bilibili.com/video/BV1aaaaaaaa1?p=2", 50); err != nil {
		t.Fatal(err)
	}
	if last := fr.gotArgs[len(fr.gotArgs)-1]; last != "https://www.bilibili.com/video/BV1aaaaaaaa1" {
		t.Fatalf("resolved %q, want the canonical part-1 URL", last)
	}
}

func TestClassifyBilibiliUnavailable(t *testing.T) {
	for _, msg := range []string{"ERROR: [BiliBili] BV1x: 啊叻？视频不见了？", "ERROR: This video is only available in mainland China", "ERROR: Video unavailable"} {
		if err := ClassifyBilibili(errors.New("yt-dlp: exit status 1: " + msg)); !errors.Is(err, ErrBilibiliUnavailable) {
			t.Errorf("%q → %v", msg, err)
		}
	}
}

func TestPushBack412(t *testing.T) {
	if !PushBack(errors.New("yt-dlp: exit status 1: ERROR: HTTP Error 412: Precondition Failed")) {
		t.Fatal("a raw 412 (e.g. from a preview download) is push-back")
	}
}

// A deleted or region-locked video in a favourites folder must not stop the
// rest: without --ignore-errors yt-dlp quits at the first bad entry.
func TestResolveListBilibiliIgnoresBadEntries(t *testing.T) {
	fr := &fakeRunner{output: fixture(t, "bilifavlist.jsonl")}
	if _, err := (&Client{Runner: fr}).ResolveList(context.Background(), "https://space.bilibili.com/123/favlist?fid=456", 50); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fr.gotArgs, "--ignore-errors") {
		t.Fatalf("no --ignore-errors in %v", fr.gotArgs)
	}
}
