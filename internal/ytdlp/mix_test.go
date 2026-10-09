package ytdlp

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// A YouTube Mix is resolved as a playlist (never --no-playlist, unlike a
// pasted watch?v=X&list=RD… link), capped, and parsed from yt-dlp's real
// flat-playlist shape: private entries dropped, live entries flagged,
// thumbnails always YouTube's hqdefault.
func TestClientMix(t *testing.T) {
	fr := &fakeRunner{output: readTestdata(t, "mix.json")}
	c := &Client{Runner: fr}
	vs, err := c.Mix(context.Background(), "seedvideo01", 50)
	if err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"--js-runtimes", "node", "--flat-playlist", "-J", "--no-warnings", "--playlist-end", "50",
		"https://www.youtube.com/watch?v=seedvideo01&list=RDseedvideo01"}
	if !reflect.DeepEqual(fr.gotArgs, wantArgs) {
		t.Fatalf("args %q", fr.gotArgs)
	}
	var ids []string
	for _, v := range vs {
		ids = append(ids, v.ID)
	}
	if strings.Join(ids, ",") != "seedvideo01,mixentry002,mixentry003,mixentry004,mixentry005,mixentry007" {
		t.Fatalf("ids %v", ids)
	}
	if vs[1].Title != "邓丽君 - 月亮代表我的心 (官方MV)" || vs[1].Channel != "邓丽君 Teresa Teng" || vs[1].DurationS != 205 ||
		vs[1].Thumbnail != "https://i.ytimg.com/vi/mixentry002/hqdefault.jpg" || vs[1].URL != "https://www.youtube.com/watch?v=mixentry002" {
		t.Fatalf("entry %+v", vs[1])
	}
	if !vs[4].Live || vs[1].Live || vs[4].DurationS != 0 {
		t.Fatalf("live flags %+v %+v", vs[4], vs[1])
	}
}

func TestClientMixCapsAndValidates(t *testing.T) {
	fr := &fakeRunner{output: readTestdata(t, "mix.json")}
	c := &Client{Runner: fr}
	vs, err := c.Mix(context.Background(), "seedvideo01", 2)
	if err != nil || len(vs) != 2 {
		t.Fatalf("%d %v", len(vs), err)
	}
	fr.gotArgs = nil
	if _, err := c.Mix(context.Background(), "--exec=rm", 5); err != ErrBadURL || fr.gotArgs != nil {
		t.Fatalf("bad id: %v %q", err, fr.gotArgs)
	}
}

func TestClientSearchTop(t *testing.T) {
	fr := &fakeRunner{output: readTestdata(t, "search.json")}
	c := &Client{Runner: fr}
	vs, err := c.SearchTop(context.Background(), " 甜蜜蜜 邓丽君 ", 5)
	if err != nil || len(vs) == 0 {
		t.Fatalf("%v %v", vs, err)
	}
	if last := fr.gotArgs[len(fr.gotArgs)-1]; last != "ytsearch5:甜蜜蜜 邓丽君" {
		t.Fatalf("query arg %q", last)
	}
	if _, err := c.SearchTop(context.Background(), "", 5); err != ErrBadQuery {
		t.Fatal(err)
	}
}

// Nice runs yt-dlp through nice(1) at low CPU priority.
func TestExecRunner_Nice(t *testing.T) {
	script := writeScript(t, "#!/bin/sh\necho \"$@\"\n")
	r := ExecRunner{Bin: func() string { return script }, Nice: true}
	out, err := r.Output(context.Background(), []string{"a", "b"})
	if err != nil || strings.TrimSpace(string(out)) != "a b" {
		t.Fatalf("%q %v", out, err)
	}
	if got := r.argv([]string{"x"}); !reflect.DeepEqual(got, []string{"nice", "-n", "10", script, "x"}) {
		t.Fatalf("argv %q", got)
	}
	if got := (ExecRunner{Bin: func() string { return script }}).argv([]string{"x"}); !reflect.DeepEqual(got, []string{script, "x"}) {
		t.Fatalf("argv %q", got)
	}
}
