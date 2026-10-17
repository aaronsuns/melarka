package ytdlp

import (
	"context"
	"slices"
	"testing"
)

func TestSearchKeepsViewCounts(t *testing.T) {
	f := &fakeRunner{output: []byte(`{"_type":"playlist","entries":[
		{"id":"aaaaaaaaaaa","title":"A","view_count":354205},
		{"id":"bbbbbbbbbbb","title":"B","view_count":null},
		{"id":"ccccccccccc","title":"C","view_count":3354579,"like_count":40000}]}`)}
	vs, err := (&Client{Runner: f}).Search(context.Background(), "流年 王菲")
	if err != nil || len(vs) != 3 {
		t.Fatalf("%+v %v", vs, err)
	}
	if vs[0].Views != 354205 || vs[1].Views != 0 || vs[2].Views != 3354579 || vs[2].Likes != 40000 {
		t.Fatalf("%+v", vs)
	}
}

func ids(vs []Video) []string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = v.ID
	}
	return out
}

// YouTube's order stays the main signal; a much more viewed video moves up a few places.
func TestPopularBlendsOrderAndViews(t *testing.T) {
	v := func(id string, views int64) Video { return Video{ID: id, Views: views} }
	got := ids(Popular([]Video{
		v("mv", 354_205), v("lossless", 436_067), v("plain", 3_354_579), v("fanclip", 2_538_923), v("hq", 34_437),
	}))
	// plain 6.53−0.6 = 5.93; mv 5.55; fanclip 6.40−0.9 = 5.50; lossless 5.64−0.3 = 5.34; hq 4.54−1.2 = 3.34
	if want := []string{"plain", "mv", "fanclip", "lossless", "hq"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	// Views unknown everywhere: YouTube's order, untouched.
	if got := ids(Popular([]Video{v("a", 0), v("b", 0), v("c", 0)})); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("no views: %v", got)
	}
	// An unknown count counts as the median of the known ones, not as zero.
	// (median of 1000 and 1M: the upper one) b 6.0−0.3 = 5.7; c 6.0−0.6 = 5.4; a 3.0
	if got := ids(Popular([]Video{v("a", 1000), v("b", 0), v("c", 1_000_000)})); !slices.Equal(got, []string{"b", "c", "a"}) {
		t.Fatalf("unknown as median: %v", got)
	}
	in := []Video{v("x", 1), v("y", 1_000_000)}
	Popular(in)
	if in[0].ID != "x" {
		t.Fatal("Popular reordered its argument")
	}
}

func TestStats(t *testing.T) {
	f := &fakeRunner{output: []byte(`{"id":"7maJOI3QMu0","title":"River","view_count":234239467,"like_count":3653497}`)}
	views, likes, err := (&Client{Runner: f}).Stats(context.Background(), "7maJOI3QMu0")
	if err != nil || views != 234239467 || likes != 3653497 {
		t.Fatalf("%d %d %v", views, likes, err)
	}
	if !slices.Contains(f.gotArgs, "--skip-download") || !slices.Contains(f.gotArgs, "--no-playlist") ||
		slices.Contains(f.gotArgs, "--flat-playlist") || f.gotArgs[len(f.gotArgs)-1] != WatchURL("7maJOI3QMu0") {
		t.Fatalf("args %v", f.gotArgs)
	}
	if _, _, err := (&Client{Runner: f}).Stats(context.Background(), "not-an-id"); err == nil {
		t.Fatal("bad id accepted")
	}
}
