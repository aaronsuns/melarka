package lastfm

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/tags"
	"github.com/aaronsuns/lark-server/internal/testutil"
)

type fakeSource struct {
	track       map[string][]Tag // "artist|track"
	artist      map[string][]Tag
	trackCalls  int
	artistCalls int
	err         error
	errFor      map[string]error // by artist, for both calls
}

func (f *fakeSource) TrackTopTags(ctx context.Context, artist, track string) ([]Tag, error) {
	f.trackCalls++
	if f.err != nil {
		return nil, f.err
	}
	if e := f.errFor[artist]; e != nil {
		return nil, e
	}
	if ts, ok := f.track[artist+"|"+track]; ok {
		return ts, nil
	}
	return nil, ErrNotFound
}

func (f *fakeSource) ArtistTopTags(ctx context.Context, artist string) ([]Tag, error) {
	f.artistCalls++
	if f.err != nil {
		return nil, f.err
	}
	if e := f.errFor[artist]; e != nil {
		return nil, e
	}
	return f.artist[artist], nil
}

var (
	trackTags  = []Tag{{"chinese", 100}, {"Mandopop", 71}, {"chillout", 40}, {"80s", 12}, {"female vocalists", 9}, {"seen live", 30}}
	artistTags = []Tag{{"mandopop", 100}, {"chinese", 60}, {"female vocalists", 55}}
)

func setup(t *testing.T) (*Worker, *fakeSource, *sql.DB) {
	d := testutil.DB(t)
	d.Exec(`INSERT INTO libraries(id,name,root) VALUES (1,'m','/m')`)
	src := &fakeSource{
		track:  map[string][]Tag{"Teresa Teng|Tian Mi Mi": trackTags},
		artist: map[string][]Tag{"Teresa Teng": artistTags},
	}
	w := &Worker{DB: d, Source: src, Tags: &tags.Store{DB: d}, Vocab: tags.Vocabulary(),
		Idle: time.Hour, ErrBackoff: time.Hour, Now: func() time.Time { return time.Unix(1000, 0) }}
	return w, src, d
}

func addTrack(d *sql.DB, id int, title, artist string, added int) {
	d.Exec(`INSERT INTO tracks(id,library_id,rel_path,size,mtime,fingerprint,status,added_at,tag_title,tag_artist)
		VALUES (?,1,?,1,1,'f','kept',?,?,?)`, id, title+".mp3", added, title, artist)
}

func tagNames(t *testing.T, w *Worker, id int64) map[string]bool {
	ts, err := w.Tags.ForTrack(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return names(ts)
}

func names(ts []tags.Tag) map[string]bool {
	m := map[string]bool{}
	for _, t := range ts {
		m[t.Name] = true
	}
	return m
}

func lastfmAt(d *sql.DB, id int) (v sql.NullInt64) {
	d.QueryRow(`SELECT lastfm_at FROM tagging_state WHERE track_id=?`, id).Scan(&v)
	return
}

func same(got map[string]bool, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, w := range want {
		if !got[w] {
			return false
		}
	}
	return true
}

func TestStepTagsFromTrack(t *testing.T) {
	w, _, d := setup(t)
	addTrack(d, 1, "Tian Mi Mi", "Teresa Teng", 1)
	did, err := w.Step(context.Background())
	if !did || err != nil {
		t.Fatalf("did=%v err=%v", did, err)
	}
	if n := tagNames(t, w, 1); !same(n, "mandarin", "mandopop", "chill", "80s") {
		t.Fatalf("tags %v", n)
	}
	if v := lastfmAt(d, 1); !v.Valid || v.Int64 != 1000 {
		t.Fatalf("lastfm_at %v", v)
	}
}

func TestArtistFallbackCachedOncePerArtist(t *testing.T) {
	w, src, d := setup(t)
	addTrack(d, 2, "Other One", "Teresa Teng", 2)
	addTrack(d, 3, "Other Two", "Teresa Teng", 1)
	for i := 0; i < 2; i++ {
		if did, err := w.Step(context.Background()); !did || err != nil {
			t.Fatalf("step %d: %v %v", i, did, err)
		}
	}
	for _, id := range []int64{2, 3} {
		if n := tagNames(t, w, id); !same(n, "mandopop", "mandarin", "female-vocal") {
			t.Fatalf("track %d tags %v", id, n)
		}
	}
	if src.artistCalls != 1 {
		t.Fatalf("ArtistTopTags called %d times, want 1", src.artistCalls)
	}
}

func TestExpiredArtistCacheRefetched(t *testing.T) {
	w, src, d := setup(t)
	w.ArtistTTL = time.Hour
	addTrack(d, 2, "Other One", "Teresa Teng", 2)
	d.Exec(`INSERT INTO lastfm_artist_tags(artist,tags,fetched_at) VALUES ('teresa teng','[]',?)`, 1000-7200)
	if _, err := w.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if src.artistCalls != 1 {
		t.Fatalf("stale cache not refetched: %d", src.artistCalls)
	}
}

func TestTombstoneSurvivesRerun(t *testing.T) {
	w, _, d := setup(t)
	addTrack(d, 1, "Tian Mi Mi", "Teresa Teng", 1)
	ctx := context.Background()
	w.Step(ctx)
	if err := w.Tags.Replace(ctx, 1, "manual", []tags.Tag{{Name: "mandopop"}, {Name: "mandarin"}, {Name: "80s"}}); err != nil {
		t.Fatal(err)
	}
	d.Exec(`UPDATE tagging_state SET lastfm_at=NULL`)
	if did, err := w.Step(ctx); !did || err != nil {
		t.Fatalf("%v %v", did, err)
	}
	if n := tagNames(t, w, 1); n["chill"] || !n["mandopop"] {
		t.Fatalf("tags %v", n)
	}
}

func TestNetworkErrorLeavesTrackUnmarked(t *testing.T) {
	w, src, d := setup(t)
	addTrack(d, 1, "Tian Mi Mi", "Teresa Teng", 1)
	src.err = fmt.Errorf("%w: network down", ErrUnavailable)
	did, err := w.Step(context.Background())
	if err == nil || did {
		t.Fatalf("did=%v err=%v", did, err)
	}
	if lastfmAt(d, 1).Valid {
		t.Fatal("track marked after a transport error")
	}
	src.err = nil
	if did, err := w.Step(context.Background()); !did || err != nil {
		t.Fatalf("%v %v", did, err)
	}
	if !lastfmAt(d, 1).Valid {
		t.Fatal("not marked after success")
	}
}

func TestNothingMappedStillMarked(t *testing.T) {
	w, src, d := setup(t)
	addTrack(d, 1, "Obscure", "Nobody", 1)
	src.track["Nobody|Obscure"] = []Tag{{"seen live", 50}, {"jazz", 3}}
	src.artist["Nobody"] = nil
	if did, err := w.Step(context.Background()); !did || err != nil {
		t.Fatalf("%v %v", did, err)
	}
	if !lastfmAt(d, 1).Valid || len(tagNames(t, w, 1)) != 0 {
		t.Fatal("expected marked with no tags")
	}
}

func TestMaxTagsAndOrder(t *testing.T) {
	w, src, d := setup(t)
	w.MaxTags = 2
	addTrack(d, 1, "T", "A", 1)
	src.track["A|T"] = []Tag{{"jazz", 90}, {"jazz music", 80}, {"blues", 70}, {"rock", 60}}
	w.Step(context.Background())
	if n := tagNames(t, w, 1); !same(n, "jazz", "blues") {
		t.Fatalf("tags %v", n)
	}
}

func TestSkipsIneligibleAndOrdersKeptFirst(t *testing.T) {
	w, _, d := setup(t)
	addTrack(d, 1, "NoArtist", "", 9)
	addTrack(d, 2, "Trashed", "Teresa Teng", 9)
	d.Exec(`UPDATE tracks SET status='trashed' WHERE id=2`)
	addTrack(d, 3, "Pending", "Teresa Teng", 50)
	d.Exec(`UPDATE tracks SET status='pending' WHERE id=3`)
	addTrack(d, 4, "Kept Old", "Teresa Teng", 1)
	if _, err := w.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !lastfmAt(d, 4).Valid || lastfmAt(d, 3).Valid || lastfmAt(d, 1).Valid || lastfmAt(d, 2).Valid {
		t.Fatal("wrong track picked")
	}
}

func TestNoWorkAndRunStops(t *testing.T) {
	w, _, _ := setup(t)
	if did, err := w.Step(context.Background()); did || err != nil {
		t.Fatalf("%v %v", did, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop")
	}
}

// A track whose answer Last.fm can't give (error 8, a 500, a garbled count)
// must not block the tracks behind it; it is retried after SkipFor.
func TestPerTrackErrorDoesNotBlockLibrary(t *testing.T) {
	w, src, d := setup(t)
	now := time.Unix(1000, 0)
	w.Now = func() time.Time { return now }
	w.SkipFor = time.Hour
	addTrack(d, 1, "Odd", "Weird Artist", 9) // newest: picked first
	addTrack(d, 2, "Tian Mi Mi", "Teresa Teng", 1)
	src.errFor = map[string]error{"Weird Artist": errors.New("lastfm: error 8: Operation failed")}
	ctx := context.Background()
	if _, err := w.Step(ctx); err == nil {
		t.Fatal("per-track error not reported")
	}
	if lastfmAt(d, 1).Valid {
		t.Fatal("failed track marked done")
	}
	if did, err := w.Step(ctx); !did || err != nil {
		t.Fatalf("second step: %v %v", did, err)
	}
	if !lastfmAt(d, 2).Valid {
		t.Fatal("track 2 blocked behind the failing track")
	}
	if did, err := w.Step(ctx); did || err != nil {
		t.Fatalf("skipped track retried too early: %v %v", did, err)
	}
	now = now.Add(2 * time.Hour)
	src.errFor = nil
	src.artist["Weird Artist"] = artistTags
	if did, err := w.Step(ctx); !did || err != nil {
		t.Fatalf("after skip expiry: %v %v", did, err)
	}
	if !lastfmAt(d, 1).Valid {
		t.Fatal("skipped track not retried after expiry")
	}
}

// An outage says nothing about the track: it stays at the head of the queue
// so an hour offline doesn't push the whole library into the skip list.
func TestOutageDoesNotSkipTracks(t *testing.T) {
	w, src, d := setup(t)
	addTrack(d, 1, "Tian Mi Mi", "Teresa Teng", 9)
	addTrack(d, 2, "Other", "Teresa Teng", 1)
	src.err = fmt.Errorf("%w: connection refused", ErrUnavailable)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := w.Step(ctx); err == nil {
			t.Fatal("outage not reported")
		}
	}
	if src.trackCalls != 3 {
		t.Fatalf("track calls %d", src.trackCalls)
	}
	if len(w.skipped()) != 0 {
		t.Fatalf("outage skipped tracks: %v", w.skipped())
	}
	src.err = nil
	if did, err := w.Step(ctx); !did || err != nil || !lastfmAt(d, 1).Valid {
		t.Fatalf("head track not retried after outage: %v %v", did, err)
	}
}
