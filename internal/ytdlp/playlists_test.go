package ytdlp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestSearchPlaylists(t *testing.T) {
	fr := &fakeRunner{output: readTestdata(t, "search_playlists.json")}
	c := &Client{Runner: fr}
	pls, err := c.SearchPlaylists(context.Background(), "邓丽君 a&b #c")
	if err != nil {
		t.Fatal(err)
	}
	want := append(c.baseArgs(), "--flat-playlist", "-J", "--no-warnings", "--playlist-end", "5",
		"https://www.youtube.com/results?search_query=%E9%82%93%E4%B8%BD%E5%90%9B+a%26b+%23c&sp=EgIQAw%253D%253D")
	if !reflect.DeepEqual(fr.gotArgs, want) {
		t.Fatalf("argv\n got %q\nwant %q", fr.gotArgs, want)
	}
	if len(pls) != 2 || pls[0].ID != "PL1F3EC94FCEA4669F" || pls[1].Channel != "Chinese Classic Songs" ||
		pls[0].URL != "https://www.youtube.com/playlist?list=PL1F3EC94FCEA4669F" || !strings.HasSuffix(pls[0].Thumbnail, "sqp=b") || pls[0].Count != 0 {
		t.Fatalf("%+v", pls)
	}
	if _, err := c.SearchPlaylists(context.Background(), "  "); !errors.Is(err, ErrBadQuery) {
		t.Fatal("empty query")
	}
}

func TestPlaylistInfoAndResolveList(t *testing.T) {
	fr := &fakeRunner{output: readTestdata(t, "playlist_info.json")}
	c := &Client{Runner: fr}
	p, err := c.PlaylistInfo(context.Background(), "PL1F3EC94FCEA4669F")
	if err != nil || p.Count != 247 || p.Title == "" || fr.gotArgs[len(fr.gotArgs)-1] != PlaylistURL("PL1F3EC94FCEA4669F") {
		t.Fatalf("%+v %v %q", p, err, fr.gotArgs)
	}
	if _, err := c.PlaylistInfo(context.Background(), "--exec=x"); err == nil {
		t.Fatal("bad id reached argv")
	}
	fr.output = readTestdata(t, "playlist.json")
	l, err := c.ResolveList(context.Background(), "https://www.youtube.com/playlist?list=PLtestlist0001", 200)
	if err != nil || l.ID != "PLtestlist0001" || l.Title != "Test list" || l.Count != 3 || len(l.Videos) != 2 {
		t.Fatalf("%+v %v", l, err)
	}
}
