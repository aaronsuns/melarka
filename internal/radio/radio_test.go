package radio

import (
	"context"
	"database/sql"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/testutil"
)

var now = time.Unix(1_800_000_000, 0)

func setup(t *testing.T, tracks int) (*Radio, *sql.DB) {
	d := testutil.DB(t)
	d.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'u','h','member',0)`)
	d.Exec(`INSERT INTO libraries(id,name,root) VALUES (1,'m','/m')`)
	for i := 1; i <= tracks; i++ {
		d.Exec(`INSERT INTO tracks(id,library_id,rel_path,size,mtime,fingerprint,duration_ms,status,added_at) VALUES (?,1,?,1,1,'f',200000,'kept',0)`, i, i)
	}
	return &Radio{DB: d, Now: func() time.Time { return now }, Rand: rand.New(rand.NewPCG(1, 2))}, d
}

func TestExclusions(t *testing.T) {
	r, d := setup(t, 6)
	d.Exec(`INSERT INTO dislikes VALUES (1,1,0)`)
	d.Exec(`INSERT INTO play_events(user_id,track_id,client_event_id,started_at,played_seconds) VALUES (1,2,'a',?,200)`, now.Add(-time.Hour).Unix())
	d.Exec(`INSERT INTO play_events(user_id,track_id,client_event_id,started_at,played_seconds,skipped) VALUES (1,3,'b',?,5,1)`, now.Add(-72*time.Hour).Unix())
	d.Exec(`UPDATE tracks SET status='trashed' WHERE id=4`)
	for i := 0; i < 50; i++ {
		ids, err := r.Next(context.Background(), 1, 10, []int64{5})
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 1 || ids[0] != 6 {
			t.Fatalf("got %v, only track 6 is eligible", ids)
		}
	}
}

func TestFavoritesWeighted(t *testing.T) {
	r, d := setup(t, 21)
	d.Exec(`INSERT INTO favorites VALUES (1,1,0)`)
	favHits := 0
	for i := 0; i < 500; i++ {
		ids, _ := r.Next(context.Background(), 1, 1, nil)
		if len(ids) == 1 && ids[0] == 1 {
			favHits++
		}
	}
	// P(fav) = 3 / (3 + 20*0.2) ≈ 0.43
	if favHits < 150 || favHits > 300 {
		t.Fatalf("favorite picked %d/500", favHits)
	}
}

// TestTagMatchWeighted proves the SQL-filtered tag match (fix for the
// review finding on the unfiltered track_tags scan) still assigns tag
// weight correctly: tracks 1 and 2 share a non-generic tag with a track
// (2) the user played in the last 7 days, so both should be picked far
// more often than an untagged filler track.
func TestTagMatchWeighted(t *testing.T) {
	r, d := setup(t, 21)
	d.Exec(`INSERT INTO tags(id,name,kind) VALUES (1,'x','other')`)
	d.Exec(`INSERT INTO track_tags(track_id,tag_id,source,confidence,removed) VALUES (1,1,'manual',1,0)`)
	d.Exec(`INSERT INTO track_tags(track_id,tag_id,source,confidence,removed) VALUES (2,1,'manual',1,0)`)
	d.Exec(`INSERT INTO play_events(user_id,track_id,client_event_id,started_at,played_seconds) VALUES (1,2,'p1',?,200)`, now.Add(-24*time.Hour).Unix())

	tagHits, fillerHits := 0, 0
	for i := 0; i < 500; i++ {
		ids, err := r.Next(context.Background(), 1, 1, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) != 1 {
			continue
		}
		switch ids[0] {
		case 1, 2:
			tagHits++
		default:
			fillerHits++
		}
	}
	// P(tag match, either track) = 2*1 / (2*1 + 19*0.2) ≈ 0.34; a single
	// filler track's share is 0.2/5.8 ≈ 0.034, i.e. an order of magnitude
	// less per track. Assert the aggregate tag-match rate is comfortably
	// ahead of what 19 filler tracks split between them (≈ everything
	// else) so the SQL rewrite didn't silently drop the tag weight.
	if tagHits < 100 || tagHits > 260 {
		t.Fatalf("tag match picked %d/500 (fillerHits=%d)", tagHits, fillerHits)
	}
}

func TestNoDuplicatesAndLimit(t *testing.T) {
	r, _ := setup(t, 30)
	ids, _ := r.Next(context.Background(), 1, 20, nil)
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] {
			t.Fatal("duplicate")
		}
		seen[id] = true
	}
	if len(ids) != 20 {
		t.Fatalf("len=%d", len(ids))
	}
}
