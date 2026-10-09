package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestIsChannelID(t *testing.T) {
	for id, want := range map[string]bool{
		"UC0e5c4U67Vm6sAVK0vxN3Uw": true, "UCRABK12_6Ie2X549K9cXS0g": true,
		"UC0e5c4U67Vm6sAVK0vxN3U": false, "0e5c4U67Vm6sAVK0vxN3Uw": false, "UC0e5c4U67Vm6sAVK0vxN3Uw/x": false,
		"--exec=UC0e5c4U67Vm6sA": false, "": false,
	} {
		if IsChannelID(id) != want {
			t.Errorf("IsChannelID(%q) = %v", id, !want)
		}
	}
}

func TestChannelPageURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://www.youtube.com/@liu-xiang":                              "https://www.youtube.com/@liu-xiang",
		"https://www.youtube.com/@liu-xiang/videos":                       "https://www.youtube.com/@liu-xiang",
		"https://m.youtube.com/channel/UC0e5c4U67Vm6sAVK0vxN3Uw/featured": "https://www.youtube.com/channel/UC0e5c4U67Vm6sAVK0vxN3Uw",
		"https://www.youtube.com/c/SomeName":                              "https://www.youtube.com/c/SomeName",
		"https://www.youtube.com/user/old.name":                           "https://www.youtube.com/user/old.name",
		"https://www.youtube.com/@%E5%88%98%E7%BF%94":                     "https://www.youtube.com/@%E5%88%98%E7%BF%94",
		"https://www.youtube.com/watch?v=rvOZh8idOrU":                     "",
		"https://www.youtube.com/playlist?list=PLabcdefghijkl":            "",
		"https://www.youtube.com/channel/UCshort":                         "",
		"https://www.youtube.com/@a%20b":                                  "",
		"https://www.youtube.com/":                                        "",
	} {
		if got := ChannelPageURL(in); got != want {
			t.Errorf("ChannelPageURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearchChannels(t *testing.T) {
	fr := &fakeRunner{output: readTestdata(t, "channel_search.json")}
	c := &Client{Runner: fr}
	chans, err := c.SearchChannels(context.Background(), " 刘翔 ")
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"--js-runtimes", "node", "--flat-playlist", "-J", "--no-warnings", "--playlist-end", "10",
		"https://www.youtube.com/results?search_query=%E5%88%98%E7%BF%94&sp=EgIQAg%253D%253D"}
	if !reflect.DeepEqual(fr.gotArgs, wantArgs) {
		t.Fatalf("args %q", fr.gotArgs)
	}
	if len(chans) != 2 {
		t.Fatalf("got %d channels: %+v", len(chans), chans)
	}
	want0 := Channel{ID: "UC0e5c4U67Vm6sAVK0vxN3Uw", Title: "刘翔的投资频道", Handle: "@liu-xiang",
		Avatar:      "https://yt3.googleusercontent.com/ytc/fake-avatar-1=s176-c-k-c0x00ffffff-no-rj-mo",
		Description: "Charlie Munger：投资最重要的三个特质是理性、理性、还是理性。", Followers: 138000}
	if chans[0] != want0 {
		t.Fatalf("channel 0 %+v", chans[0])
	}
	if chans[1].ID != "UCRABK12_6Ie2X549K9cXS0g" || chans[1].Avatar != "" || chans[1].Handle != "@wwcast" {
		t.Fatalf("channel 1 %+v", chans[1])
	}
	if _, err := c.SearchChannels(context.Background(), "  "); err != ErrBadQuery {
		t.Fatal(err)
	}
}

func TestResolveChannelFromHandle(t *testing.T) {
	fr := &fakeRunner{output: readTestdata(t, "channel_page.json")}
	c := &Client{Runner: fr}
	ch, err := c.ResolveChannel(context.Background(), "http://youtube.com/@liu-xiang/videos")
	if err != nil {
		t.Fatal(err)
	}
	if last := fr.gotArgs[len(fr.gotArgs)-1]; last != "https://www.youtube.com/@liu-xiang" {
		t.Fatalf("resolved %q", last)
	}
	if !strings.Contains(strings.Join(fr.gotArgs, " "), "--playlist-end 1") {
		t.Fatalf("args %q", fr.gotArgs)
	}
	want := Channel{ID: "UC0e5c4U67Vm6sAVK0vxN3Uw", Title: "刘翔的投资频道", Handle: "@liu-xiang",
		Avatar:      "https://yt3.googleusercontent.com/ytc/fake-avatar-1=s0",
		Description: "Charlie Munger：投资最重要的三个特质是理性、理性、还是理性。", Followers: 138000}
	if ch != want {
		t.Fatalf("%+v", ch)
	}
}

func TestResolveChannelFromVideoLink(t *testing.T) {
	info, page := readTestdata(t, "video_info.txt"), readTestdata(t, "channel_page.json")
	fr := &fakeRunner{outFor: func(args []string) ([]byte, error) {
		if args[len(args)-1] == "https://www.youtube.com/watch?v=rvOZh8idOrU" {
			return info, nil
		}
		if args[len(args)-1] == "https://www.youtube.com/channel/UC0e5c4U67Vm6sAVK0vxN3Uw" {
			return page, nil
		}
		return nil, errors.New("unexpected " + strings.Join(args, " "))
	}}
	c := &Client{Runner: fr}
	ch, err := c.ResolveChannel(context.Background(), "https://youtu.be/rvOZh8idOrU?t=10")
	if err != nil || ch.ID != "UC0e5c4U67Vm6sAVK0vxN3Uw" || ch.Title != "刘翔的投资频道" {
		t.Fatalf("%+v %v", ch, err)
	}
	if len(fr.calls) != 2 {
		t.Fatalf("calls %q", fr.calls)
	}
	for _, bad := range []string{"https://www.youtube.com/playlist?list=PLabcdefghijkl", "https://example.com/@x", "--exec=x"} {
		fr.calls = nil
		if _, err := c.ResolveChannel(context.Background(), bad); err != ErrBadURL || len(fr.calls) != 0 {
			t.Errorf("%q: %v, %d calls", bad, err, len(fr.calls))
		}
	}
}

func TestResolveChannelWithoutChannelID(t *testing.T) {
	fr := &fakeRunner{output: []byte(`{"_type":"playlist","id":"x","title":"Mix","entries":[]}`)}
	c := &Client{Runner: fr}
	if _, err := c.ResolveChannel(context.Background(), "https://www.youtube.com/@nobody"); err != ErrNoChannel {
		t.Fatal(err)
	}
}

func TestVideoInfo(t *testing.T) {
	fr := &fakeRunner{output: readTestdata(t, "video_info.txt")}
	c := &Client{Runner: fr}
	in, err := c.VideoInfo(context.Background(), "rvOZh8idOrU")
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"--js-runtimes", "node", "--no-warnings", "--no-playlist", "--skip-download",
		"--ignore-no-formats-error", "-O",
		"%(.{id,title,channel,channel_id,duration,live_status,availability,media_type,timestamp,description})j",
		"https://www.youtube.com/watch?v=rvOZh8idOrU"}
	if !reflect.DeepEqual(fr.gotArgs, wantArgs) {
		t.Fatalf("args %q", fr.gotArgs)
	}
	want := Info{ID: "rvOZh8idOrU", Title: "投行说现在像极了1999；美债要崩?", Channel: "刘翔的投资频道",
		ChannelID: "UC0e5c4U67Vm6sAVK0vxN3Uw", DurationS: 880, LiveStatus: "not_live", Availability: "public",
		MediaType: "video", Timestamp: 1790467282, Description: "保证物超所值\nhttps://www.patreon.com/liuxiang"}
	if in != want {
		t.Fatalf("%+v", in)
	}
	if _, err := c.VideoInfo(context.Background(), "--exec=rm -rf"); err != ErrBadURL {
		t.Fatal(err)
	}
	fr.output = []byte(`{"id": "otherid0000", "title": "x"}`)
	if _, err := c.VideoInfo(context.Background(), "rvOZh8idOrU"); err == nil {
		t.Fatal("an answer about another video must be an error")
	}
}

// A scheduled premiere has no formats yet; without the
// flag yt-dlp errors out instead of printing its metadata.
func TestVideoInfoIgnoresNoFormatsError(t *testing.T) {
	fr := &fakeRunner{output: readTestdata(t, "video_info.txt")}
	if _, err := (&Client{Runner: fr}).VideoInfo(context.Background(), "rvOZh8idOrU"); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range fr.gotArgs {
		if a == "--ignore-no-formats-error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("argv lacks --ignore-no-formats-error: %q", fr.gotArgs)
	}
}

// Only failures that retrying can never fix are permanent.
func TestUnavailable(t *testing.T) {
	for msg, want := range map[string]bool{
		"yt-dlp: exit status 1: ERROR: [youtube] JVsqDH7MY18: Join this channel to get access to members-only content like this video": true,
		"yt-dlp: exit status 1: ERROR: [youtube] x: Private video. Sign in if you've been granted access":                              true,
		"yt-dlp: exit status 1: ERROR: [youtube] x: Video unavailable. This video has been removed by the uploader":                    true,
		"yt-dlp: exit status 1: ERROR: [youtube] x: Sign in to confirm your age. This video may be inappropriate for some users.":      true,
		"yt-dlp: exit status 1: ERROR: [youtube] x: Sign in to confirm you're not a bot. Use --cookies-from-browser":                   false,
		"yt-dlp: exit status 1: ERROR: [youtube] x: Video unavailable. This content isn't available, try again later.":                 false,
		"yt-dlp: exit status 1: ERROR: unable to download video data: HTTP Error 403: Forbidden":                                       false,
		"context deadline exceeded": false,
	} {
		if got := Unavailable(errors.New(msg)); got != want {
			t.Errorf("Unavailable(%q) = %v", msg, got)
		}
	}
	if Unavailable(nil) {
		t.Fatal("nil")
	}
}

func TestSearchAndMixCarryChannelID(t *testing.T) {
	fr := &fakeRunner{output: readTestdata(t, "mix.json")}
	vs, err := (&Client{Runner: fr}).Mix(context.Background(), "seedvideo01", 50)
	if err != nil {
		t.Fatal(err)
	}
	if vs[0].ChannelID != "UCfakechannel00000000001" {
		t.Fatalf("%+v", vs[0])
	}
}

// Only YouTube pushing back stops a poll's detail reads; any
// other failure is that one video's.
func TestPushBack(t *testing.T) {
	for msg, want := range map[string]bool{
		"yt-dlp: exit status 1: ERROR: [youtube] x: Sign in to confirm you're not a bot. Use --cookies-from-browser":                true,
		"yt-dlp: exit status 1: ERROR: [youtube] x: Video unavailable. This content isn't available, try again later.":              true,
		"yt-dlp: exit status 1: ERROR: Unable to download API page: HTTP Error 429: Too Many Requests":                              true,
		"yt-dlp: exit status 1: ERROR: [youtube] x: Premieres in 2 hours":                                                           false,
		"yt-dlp: exit status 1: ERROR: [youtube] x: Private video. Sign in if you've been granted access":                           false,
		"yt-dlp: exit status 1: ERROR: Unable to download webpage: <urlopen error [Errno -3] Temporary failure in name resolution>": true,
		"yt-dlp: exit status 1: ERROR: [youtube] x: Read timed out.":                                                                true,
		"yt-dlp: exit status 1: ERROR: [Errno 104] Connection reset by peer":                                                        true,
		"yt-dlp: exit status 1: ERROR: unable to download video data: HTTP Error 503: Service Unavailable":                          true,
		"yt-dlp: exit status 1: ERROR: [youtube] x: Requested format is not available":                                              false,
		"yt-dlp: exit status 1: ERROR: unable to download video data: HTTP Error 403: Forbidden":                                    false,
	} {
		if got := PushBack(errors.New(msg)); got != want {
			t.Errorf("PushBack(%q) = %v", msg, got)
		}
	}
	if PushBack(nil) {
		t.Fatal("nil")
	}
	for msg, want := range map[string]bool{
		"yt-dlp: exit status 1: ERROR: unable to download video data: HTTP Error 403: Forbidden":          true,
		"yt-dlp: exit status 1: ERROR: unable to download video data: HTTP Error 404: Not Found":          false,
		"yt-dlp: exit status 1: ERROR: [youtube] x: Private video. Sign in if you've been granted access": false,
	} {
		if got := Forbidden(errors.New(msg)); got != want {
			t.Errorf("Forbidden(%q) = %v", msg, got)
		}
	}
	if Forbidden(nil) {
		t.Fatal("nil is not forbidden")
	}
	if !PushBack(fmt.Errorf("channels: yt-dlp timed out: %w", context.DeadlineExceeded)) {
		t.Fatal("a timeout is push-back")
	}
}
