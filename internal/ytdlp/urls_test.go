package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

const vid = "dQw4w9WgXcQ" // a real-shaped 11-char YouTube id

// A song link copied while it plays inside a playlist or a mix must queue
// that one song, not expand the list: --no-playlist is added whenever the URL
// itself names one video. Only a /playlist?list= URL expands.
func TestResolve_NoPlaylistForSingleVideoShapes(t *testing.T) {
	cases := []struct {
		url    string
		single bool
	}{
		{"https://www.youtube.com/watch?v=" + vid, true},
		{"https://www.youtube.com/watch?v=" + vid + "&list=PLabcdefghijklmnop", true},
		{"https://www.youtube.com/watch?v=" + vid + "&list=RD" + vid + "&start_radio=1", true},
		{"https://music.youtube.com/watch?v=" + vid + "&list=RDAMVM" + vid, true},
		{"https://youtu.be/" + vid + "?list=PLabcdefghijklmnop", true},
		{"https://youtu.be/" + vid, true},
		{"https://www.youtube.com/shorts/" + vid, true},
		{"https://www.youtube.com/live/" + vid + "?si=x", true},
		{"https://www.youtube.com/playlist?list=PLabcdefghijklmnop", false},
		{"https://music.youtube.com/playlist?list=OLAK5uy_abc", false},
	}
	for _, c := range cases {
		fr := &fakeRunner{output: []byte(`{"_type":"playlist","entries":[]}`)}
		cl := &Client{Runner: fr}
		if _, err := cl.Resolve(context.Background(), c.url, 200); err != nil {
			t.Fatalf("%s: %v", c.url, err)
		}
		if got := slices.Contains(fr.gotArgs, "--no-playlist"); got != c.single {
			t.Errorf("%s: --no-playlist present=%v, want %v (argv %v)", c.url, got, c.single, fr.gotArgs)
		}
	}
}

// Channel, playlist and tab entries (a channel page or a search can list
// them) are not videos: only 11-char video ids survive, and their URL is
// always the canonical watch page, never the entry's own url.
func TestResolve_DropsNonVideoEntries(t *testing.T) {
	json := []byte(`{"_type":"playlist","entries":[
		{"id":"UCabcdefghijklmnopqrstuv","title":"Some Channel","url":"https://www.youtube.com/channel/UCabcdefghijklmnopqrstuv"},
		{"id":"PLabcdefghijklmnopqrstuvwxyz012345","title":"Some Playlist","url":"https://www.youtube.com/playlist?list=PLabcdefghijklmnopqrstuvwxyz012345"},
		{"id":"` + vid + `","title":"Song","channel":"C","url":"https://www.youtube.com/watch?v=` + vid + `"},
		{"id":"abcdefghij_","title":"Short","channel":"C","url":"https://www.youtube.com/shorts/abcdefghij_"},
		{"id":"UCabcdefghijklmnopqrstuv_videos","title":"Videos","url":"https://www.youtube.com/channel/UCabcdefghijklmnopqrstuv/videos"}
	]}`)
	fr := &fakeRunner{output: json}
	cl := &Client{Runner: fr}
	videos, err := cl.Resolve(context.Background(), "https://www.youtube.com/@someone", 200)
	if err != nil {
		t.Fatal(err)
	}
	var ids, urls []string
	for _, v := range videos {
		ids = append(ids, v.ID)
		urls = append(urls, v.URL)
	}
	if !reflect.DeepEqual(ids, []string{vid, "abcdefghij_"}) {
		t.Fatalf("ids = %v, want only the two video entries", ids)
	}
	want := []string{"https://www.youtube.com/watch?v=" + vid, "https://www.youtube.com/watch?v=abcdefghij_"}
	if !reflect.DeepEqual(urls, want) {
		t.Fatalf("urls = %v, want %v", urls, want)
	}
}

// Shorts in search results come back as /shorts/<id> URLs: normalised to
// the watch page so they download (and pass the API's id check) like any
// other video; non-video entries are dropped from search results too.
func TestSearch_NormalisesShortsAndDropsNonVideos(t *testing.T) {
	json := []byte(`{"_type":"playlist","entries":[
		{"id":"abcdefghij_","title":"Short","channel":"C","url":"https://www.youtube.com/shorts/abcdefghij_"},
		{"id":"UCabcdefghijklmnopqrstuv","title":"Channel","url":"https://www.youtube.com/channel/UCabcdefghijklmnopqrstuv"},
		{"id":"` + vid + `","title":"Song","channel":"C","url":"https://www.youtube.com/watch?v=` + vid + `"}
	]}`)
	fr := &fakeRunner{output: json}
	cl := &Client{Runner: fr}
	videos, err := cl.Search(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if len(videos) != 2 || videos[0].URL != "https://www.youtube.com/watch?v=abcdefghij_" || videos[1].ID != vid {
		t.Fatalf("videos = %+v", videos)
	}
}

func TestVideoIDFromURL(t *testing.T) {
	cases := map[string]string{
		"https://www.youtube.com/watch?v=" + vid:               vid,
		"https://www.youtube.com/watch?v=" + vid + "&list=PL1": vid,
		"https://youtu.be/" + vid:                              vid,
		"https://youtu.be/" + vid + "?si=abc":                  vid,
		"https://www.youtube.com/shorts/" + vid:                vid,
		"https://www.youtube.com/live/" + vid:                  vid,
		"https://www.youtube.com/embed/" + vid:                 vid,
		"https://www.youtube.com/playlist?list=PL1":            "",
		"https://www.youtube.com/@someone":                     "",
		"https://www.youtube.com/shorts/":                      "",
	}
	for u, want := range cases {
		if got := VideoIDFromURL(u); got != want {
			t.Errorf("VideoIDFromURL(%q) = %q, want %q", u, got, want)
		}
	}
}

// Every search/resolve/download invocation enables node as yt-dlp's JS
// runtime (needed for YouTube's signature challenges) and, when configured,
// pins the cache dir.
func TestJSRuntimeAndCacheDirInEveryInvocation(t *testing.T) {
	fr := &fakeRunner{output: []byte(`{"_type":"playlist","entries":[]}`), lines: []string{"/x/a.m4a"}}
	cl := &Client{Runner: fr, CacheDir: "/data/cache/yt-dlp"}
	want := []string{"--js-runtimes", "node", "--cache-dir", "/data/cache/yt-dlp"}
	check := func(name string) {
		t.Helper()
		if len(fr.gotArgs) < 4 || !reflect.DeepEqual(fr.gotArgs[:4], want) {
			t.Errorf("%s argv = %v, want prefix %v", name, fr.gotArgs, want)
		}
	}
	cl.Search(context.Background(), "x")
	check("search")
	cl.Resolve(context.Background(), "https://youtu.be/"+vid, 1)
	check("resolve")
	cl.Download(context.Background(), Video{ID: vid, URL: "https://www.youtube.com/watch?v=" + vid}, "/x/a", nil)
	check("download")

	// Without CacheDir, no --cache-dir flag (yt-dlp falls back to XDG_CACHE_HOME).
	cl.CacheDir = ""
	cl.Search(context.Background(), "x")
	if slices.Contains(fr.gotArgs, "--cache-dir") || !slices.Contains(fr.gotArgs, "--js-runtimes") {
		t.Errorf("no CacheDir: argv = %v", fr.gotArgs)
	}
}

// exitErr mimics *exec.ExitError's ExitCode for the fake runner.
type exitErr struct{ code int }

func (e exitErr) Error() string { return fmt.Sprintf("exit status %d", e.code) }
func (e exitErr) ExitCode() int { return e.code }

// --max-downloads 1 makes yt-dlp exit 101 after a successful download: with
// a final path reported, that is success, not a failure.
func TestDownload_MaxDownloadsExit101IsSuccess(t *testing.T) {
	fr := &fakeRunner{lines: []string{"/x/a.m4a"}, streamErr: fmtWrap(exitErr{101})}
	cl := &Client{Runner: fr}
	p, err := cl.Download(context.Background(), Video{ID: vid, URL: "https://www.youtube.com/watch?v=" + vid}, "/x/a", nil)
	if err != nil || p != "/x/a.m4a" {
		t.Fatalf("Download = %q, %v; want /x/a.m4a, nil", p, err)
	}
	if i := slices.Index(fr.gotArgs, "--max-downloads"); i < 0 || fr.gotArgs[i+1] != "1" {
		t.Fatalf("argv lacks --max-downloads 1: %v", fr.gotArgs)
	}
	// 101 without a final path is still a failure, as is any other code.
	fr.lines = nil
	if _, err := cl.Download(context.Background(), Video{ID: vid, URL: "https://www.youtube.com/watch?v=" + vid}, "/x/a", nil); err == nil {
		t.Fatal("101 without a final path: want error")
	}
	fr.lines, fr.streamErr = []string{"/x/a.m4a"}, fmtWrap(exitErr{1})
	if _, err := cl.Download(context.Background(), Video{ID: vid, URL: "https://www.youtube.com/watch?v=" + vid}, "/x/a", nil); err == nil {
		t.Fatal("exit 1: want error")
	}
}

func fmtWrap(err error) error { return errors.Join(errors.New("yt-dlp"), err) }

func TestListIDFromURL(t *testing.T) {
	cases := []struct{ url, want string }{
		{"https://www.youtube.com/watch?v=IiFm7AWP9n4&list=PL1F3EC94FCEA4669F", "PL1F3EC94FCEA4669F"},
		{"https://www.youtube.com/playlist?list=OLAK5uy_kmPRjHDECIcuVwnKsx2Ng7fyNgFK", "OLAK5uy_kmPRjHDECIcuVwnKsx2Ng7fyNgFK"},
		{"https://www.youtube.com/watch?v=x&list=RDIiFm7AWP9n4", "RDIiFm7AWP9n4"},
		{"https://www.youtube.com/playlist?list=--exec", ""},
		{"https://www.youtube.com/playlist?list=short", ""},
		{"https://www.youtube.com/watch?v=IiFm7AWP9n4", ""},
	}
	for _, c := range cases {
		if got := ListIDFromURL(c.url); got != c.want {
			t.Errorf("%s: got %q want %q", c.url, got, c.want)
		}
	}
	if !IsMixID("RDIiFm7AWP9n4") || IsMixID("PL1F3EC94FCEA4669F") {
		t.Fatal("IsMixID")
	}
}

func TestThumbnailURL(t *testing.T) {
	if got := ThumbnailURL("dQw4w9WgXcQ"); got != "https://i.ytimg.com/vi/dQw4w9WgXcQ/hqdefault.jpg" {
		t.Fatal(got)
	}
}

func TestSetThumbnailBase(t *testing.T) {
	defer SetThumbnailBase("https://i.ytimg.com/vi")
	SetThumbnailBase("")
	if got := ThumbnailURL("abcdefghijk"); got != "https://i.ytimg.com/vi/abcdefghijk/hqdefault.jpg" {
		t.Fatalf("empty base: %s", got)
	}
	SetThumbnailBase("http://127.0.0.1:4701/vi/")
	if got := ThumbnailURL("abcdefghijk"); got != "http://127.0.0.1:4701/vi/abcdefghijk/hqdefault.jpg" {
		t.Fatalf("local base: %s", got)
	}
}
