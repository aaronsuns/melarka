package ytdlp

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// A download also prints the video's tags, categories and description (LARKMETA): no extra run.
func TestDownloadMetaReadsTagsInTheSameRun(t *testing.T) {
	f := &fakeRunner{lines: []string{
		`LARKMETA {"tags":["王菲","流年","Faye Wong","Cantopop"],"categories":["Music"],"description":"《流年》作曲：陈小霞"}`,
		"LARKPROG  50.0%",
		"/music/youtube/x/流年 [mjuS9shGYhE].m4a",
	}}
	c := &Client{Runner: f}
	path, meta, err := c.DownloadMeta(context.Background(), Video{ID: "mjuS9shGYhE", URL: WatchURL("mjuS9shGYhE")}, "/music/youtube/x/流年 [mjuS9shGYhE]", nil)
	if err != nil || path != "/music/youtube/x/流年 [mjuS9shGYhE].m4a" {
		t.Fatalf("%q %v", path, err)
	}
	if !slices.Equal(meta.Tags, []string{"王菲", "流年", "Faye Wong", "Cantopop"}) || !slices.Equal(meta.Categories, []string{"Music"}) || meta.Description != "《流年》作曲：陈小霞" {
		t.Fatalf("%+v", meta)
	}
	if !strings.Contains(strings.Join(f.gotArgs, " "), "before_dl:LARKMETA %(.{tags,categories,description})j") {
		t.Fatalf("args %v", f.gotArgs)
	}
}

func TestDownloadMetaWithoutTheLineIsEmpty(t *testing.T) {
	f := &fakeRunner{lines: []string{"/music/youtube/x/a [abcdefghijk].m4a"}}
	_, meta, err := (&Client{Runner: f}).DownloadMeta(context.Background(), Video{ID: "abcdefghijk", URL: WatchURL("abcdefghijk")}, "/music/youtube/x/a [abcdefghijk]", nil)
	if err != nil || meta.Tags != nil || meta.Description != "" {
		t.Fatalf("%+v %v", meta, err)
	}
}

func TestVideoMeta(t *testing.T) {
	f := &fakeRunner{output: []byte(`{"id":"mjuS9shGYhE","tags":["王菲"],"categories":["Music"],"description":"d"}` + "\n")}
	m, err := (&Client{Runner: f}).VideoMeta(context.Background(), "mjuS9shGYhE")
	if err != nil || !slices.Equal(m.Tags, []string{"王菲"}) || m.Description != "d" {
		t.Fatalf("%+v %v", m, err)
	}
	if !slices.Contains(f.gotArgs, "--skip-download") || f.gotArgs[len(f.gotArgs)-1] != WatchURL("mjuS9shGYhE") {
		t.Fatalf("args %v", f.gotArgs)
	}
	if _, err := (&Client{Runner: f}).VideoMeta(context.Background(), "bad"); err == nil {
		t.Fatal("bad id accepted")
	}
}
