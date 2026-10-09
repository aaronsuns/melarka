package personal

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/aaronsuns/lark-server/internal/testutil"
)

func seed(t *testing.T) (*Store, *sql.DB) {
	d := testutil.DB(t)
	d.Exec(`INSERT INTO users(id,username,password_hash,role,created_at) VALUES (1,'a','h','admin',0),(2,'b','h','member',0)`)
	d.Exec(`INSERT INTO libraries(id,name,root) VALUES (1,'main','/m')`)
	for id := 1; id <= 3; id++ {
		d.Exec(`INSERT INTO tracks(id,library_id,rel_path,size,mtime,fingerprint,duration_ms,status,added_at) VALUES (?,1,?,1,1,'f',200000,'pending',0)`, id, id)
	}
	return &Store{DB: d}, d
}

func TestFavoriteDislikeExclusive(t *testing.T) {
	s, d := seed(t)
	ctx := context.Background()
	if err := s.SetFavorite(ctx, 1, 1, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDislike(ctx, 1, 1, true); err != nil {
		t.Fatal(err)
	}
	var f, dl int
	d.QueryRow(`SELECT COUNT(*) FROM favorites`).Scan(&f)
	d.QueryRow(`SELECT COUNT(*) FROM dislikes`).Scan(&dl)
	if f != 0 || dl != 1 {
		t.Fatalf("fav=%d dislike=%d", f, dl)
	}
	if err := s.SetFavorite(ctx, 1, 999, true); err != ErrNotFound {
		t.Fatalf("unknown track err=%v", err)
	}
	if err := s.SetFavorite(ctx, 1, 2, false); err != nil {
		t.Fatal("unfavorite of non-favorite must be a no-op")
	}
}

func TestPlaylistsOwnership(t *testing.T) {
	s, _ := seed(t)
	ctx := context.Background()
	p, err := s.CreatePlaylist(ctx, 1, "车上", []int64{3, 1, 1})
	if err != nil || p.TrackCount != 3 {
		t.Fatalf("%+v %v", p, err)
	}
	_, ids, _ := s.Playlist(ctx, 1, p.ID)
	if len(ids) != 3 || ids[0] != 3 || ids[2] != 1 {
		t.Fatalf("order/duplicates lost: %v", ids)
	}
	if _, _, err := s.Playlist(ctx, 2, p.ID); err != ErrNotFound {
		t.Fatal("other user can read playlist")
	}
	if err := s.UpdatePlaylist(ctx, 2, p.ID, nil, &[]int64{}); err != ErrNotFound {
		t.Fatal("other user can edit playlist")
	}
	name := "通勤"
	if err := s.UpdatePlaylist(ctx, 1, p.ID, &name, &[]int64{2}); err != nil {
		t.Fatal(err)
	}
	ls, _ := s.Playlists(ctx, 1)
	if len(ls) != 1 || ls[0].Name != "通勤" || ls[0].TrackCount != 1 {
		t.Fatalf("%+v", ls)
	}
	if err := s.DeletePlaylist(ctx, 1, p.ID); err != nil {
		t.Fatal(err)
	}
}

// Review fix round 1, finding 1: a whitespace-only name must not slip past
// TrimSpace as a valid name, and must surface as a caller-fixable error
// (ErrInvalid), not a generic storage error.
func TestPlaylistNameValidation(t *testing.T) {
	s, _ := seed(t)
	ctx := context.Background()
	if _, err := s.CreatePlaylist(ctx, 1, "   ", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("create with whitespace name err=%v", err)
	}
	p, err := s.CreatePlaylist(ctx, 1, "ok", nil)
	if err != nil {
		t.Fatal(err)
	}
	name := "   "
	if err := s.UpdatePlaylist(ctx, 1, p.ID, &name, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("update with whitespace name err=%v", err)
	}
}

// Review fix round 1, finding 3: an unknown track id in a playlist write
// must be reported as ErrNotFound, not silently dropped or a storage error.
func TestPlaylistUnknownTrack(t *testing.T) {
	s, _ := seed(t)
	ctx := context.Background()
	if _, err := s.CreatePlaylist(ctx, 1, "x", []int64{999}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("create with unknown track err=%v", err)
	}
	p, err := s.CreatePlaylist(ctx, 1, "y", []int64{1})
	if err != nil {
		t.Fatal(err)
	}
	bad := []int64{999}
	if err := s.UpdatePlaylist(ctx, 1, p.ID, nil, &bad); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update with unknown track err=%v", err)
	}
	// the failed update must not have touched the existing items
	_, ids, _ := s.Playlist(ctx, 1, p.ID)
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("failed update mutated playlist: %v", ids)
	}
}

// Review focus 2: the phone replays a batch after a lost response.
func TestPlayEventsIdempotentAndKeepRule(t *testing.T) {
	s, d := seed(t)
	ctx := context.Background()
	evs := []PlayEvent{
		{ClientEventID: "e1", TrackID: 1, StartedAt: 10, PlayedSeconds: 150}, // ≥ 50 % of 200 s
		{ClientEventID: "e2", TrackID: 1, StartedAt: 20, PlayedSeconds: 30, Skipped: true},
		{ClientEventID: "e3", TrackID: 999, StartedAt: 30, PlayedSeconds: 100}, // unknown track: ignored
	}
	n, err := s.RecordPlays(ctx, 1, 0, evs)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	n, _ = s.RecordPlays(ctx, 1, 0, evs) // replay
	if n != 0 {
		t.Fatalf("replay accepted %d", n)
	}
	var plays int
	var status string
	d.QueryRow(`SELECT keep_plays, status FROM tracks WHERE id=1`).Scan(&plays, &status)
	if plays != 1 || status != "pending" {
		t.Fatalf("plays=%d status=%s", plays, status)
	}
	s.RecordPlays(ctx, 2, 0, []PlayEvent{{ClientEventID: "x1", TrackID: 1, PlayedSeconds: 240}, {ClientEventID: "x2", TrackID: 1, PlayedSeconds: 101}})
	d.QueryRow(`SELECT keep_plays, status FROM tracks WHERE id=1`).Scan(&plays, &status)
	if plays != 3 || status != "kept" {
		t.Fatalf("after 3 plays: plays=%d status=%s", plays, status)
	}
}

func TestQueueLastWriterWins(t *testing.T) {
	s, _ := seed(t)
	ctx := context.Background()
	q, err := s.Queue(ctx, 1)
	if err != nil || q.Version != 0 || len(q.TrackIDs) != 0 {
		t.Fatalf("%+v %v", q, err)
	}
	q1, _ := s.SaveQueue(ctx, 1, Queue{TrackIDs: []int64{1, 2, 3}, CurrentIndex: 1, PositionMS: 5000}, "iphone-private")
	q2, _ := s.SaveQueue(ctx, 1, Queue{TrackIDs: []int64{3}, CurrentIndex: 0}, "iphone-work")
	if q1.Version != 1 || q2.Version != 2 {
		t.Fatalf("versions %d %d", q1.Version, q2.Version)
	}
	got, _ := s.Queue(ctx, 1)
	if got.UpdatedBy != "iphone-work" || len(got.TrackIDs) != 1 {
		t.Fatalf("%+v", got)
	}
	if _, err := s.SaveQueue(ctx, 1, Queue{TrackIDs: []int64{1}, CurrentIndex: 5}, "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("index out of range: err=%v, want ErrInvalid", err)
	}
}
