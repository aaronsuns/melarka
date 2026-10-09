package download

import (
	"context"
	"slices"
	"testing"
	"time"
)

func playlistTitles(t *testing.T, e *env, playlistID int64) []string {
	t.Helper()
	rows, err := e.store.DB.Query(`SELECT COALESCE(NULLIF(o.title,''), tr.tag_title, tr.rel_path) FROM playlist_items i
		JOIN tracks tr ON tr.id=i.track_id LEFT JOIN track_overrides o ON o.track_id=tr.id WHERE i.playlist_id=? ORDER BY i.position`, playlistID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// waitTitles polls until playlistID holds want: a job turns "done" a moment
// before its worker places the track in the requesters' playlists.
func waitTitles(t *testing.T, e *env, playlistID int64, want []string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Equal(playlistTitles(t, e, playlistID), want) {
		if time.Now().After(deadline) {
			t.Fatalf("playlist %d = %v, want %v", playlistID, playlistTitles(t, e, playlistID), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// seedLooseTrack inserts a track in e.lib that no download produced (a song
// the user adds to a playlist by hand) and returns its id.
func seedLooseTrack(t *testing.T, e *env, rel, title string) int64 {
	t.Helper()
	r, err := e.store.DB.Exec(`INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,status,added_at) VALUES (?,?,1,0,?,'kept',0)`,
		e.lib.ID, rel, "fp-"+rel)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := r.LastInsertId()
	if _, err := e.store.DB.Exec(`INSERT INTO track_overrides(track_id,title) VALUES (?,?)`, id, title); err != nil {
		t.Fatal(err)
	}
	return id
}

func mustExec(t *testing.T, e *env, q string, args ...any) {
	t.Helper()
	if _, err := e.store.DB.Exec(q, args...); err != nil {
		t.Fatal(err)
	}
}

const listURL = "https://www.youtube.com/playlist?list=PLtestlist0001"

func TestListBecomesOrderedPlaylist(t *testing.T) {
	e := newEnv(t, true)
	e.fake.listID, e.fake.listTitle = "PLtestlist0001", "车上听"
	e.fake.entries = []entry{{"a0000000000", "A", "Ch"}, {"b0000000000", "B", "Ch"}, {"c0000000000", "C", "Ch"}}
	e.fake.block["a0000000000"] = true // c finishes before a
	e.start(t)
	ctx := context.Background()
	res, err := e.svc.EnqueueURL(ctx, e.alice, listURL)
	if err != nil || res.Playlist == nil || res.Playlist.Name != "车上听" || res.Playlist.ListID != "PLtestlist0001" || len(res.Jobs) != 3 {
		t.Fatalf("%+v %v", res, err)
	}
	e.waitStarted(t)
	e.waitStatus(t, res.Jobs[2].ID, StatusDone)
	e.waitStatus(t, res.Jobs[1].ID, StatusDone)
	waitTitles(t, e, res.Playlist.PlaylistID, []string{"B", "C"})
	close(e.fake.release)
	e.waitStatus(t, res.Jobs[0].ID, StatusDone)
	waitTitles(t, e, res.Playlist.PlaylistID, []string{"A", "B", "C"})

	// The user drops C and appends a song of their own; re-downloading keeps it and fetches nothing.
	own := seedLooseTrack(t, e, "own.m4a", "Mine")
	pid := res.Playlist.PlaylistID
	mustExec(t, e, `DELETE FROM playlist_items WHERE playlist_id=? AND position=2`, pid)
	mustExec(t, e, `INSERT INTO playlist_items VALUES (?,2,?)`, pid, own)
	streams := len(e.fake.calls())
	again, err := e.svc.EnqueueURL(ctx, e.alice, listURL)
	if err != nil || again.Playlist == nil || again.Playlist.PlaylistID != pid {
		t.Fatalf("must reuse the playlist: %+v %v", again.Playlist, err)
	}
	if len(e.fake.calls()) != streams {
		t.Fatal("re-download fetched something already in the library")
	}
	if got := playlistTitles(t, e, pid); !slices.Equal(got, []string{"A", "B", "C", "Mine"}) {
		t.Fatalf("after re-download: %v", got)
	}

	// Deleting the Lark playlist and downloading again recreates it, filled at once.
	mustExec(t, e, `DELETE FROM playlists WHERE id=?`, pid)
	third, err := e.svc.EnqueueURL(ctx, e.alice, listURL)
	if err != nil || third.Playlist == nil || third.Playlist.PlaylistID == pid ||
		!slices.Equal(playlistTitles(t, e, third.Playlist.PlaylistID), []string{"A", "B", "C"}) {
		t.Fatalf("recreated: %+v %v", third.Playlist, err)
	}

	// Bob gets his own playlist; no extra downloads.
	bob, err := e.svc.EnqueueURL(ctx, e.bob, listURL)
	if err != nil || bob.Playlist == nil || bob.Playlist.PlaylistID == third.Playlist.PlaylistID ||
		len(playlistTitles(t, e, bob.Playlist.PlaylistID)) != 3 || len(e.fake.calls()) != streams {
		t.Fatalf("bob %+v %v", bob.Playlist, err)
	}
	var owner int64
	e.store.DB.QueryRow(`SELECT user_id FROM playlists WHERE id=?`, bob.Playlist.PlaylistID).Scan(&owner)
	if owner != e.bob {
		t.Fatalf("bob's playlist belongs to %d", owner)
	}
}

// The YouTube list's new order wins on re-download (a reordered or grown
// list), and a private/deleted entry is simply skipped.
func TestListReorderAndPrivateEntries(t *testing.T) {
	e := newEnv(t, true)
	e.fake.listID, e.fake.listTitle = "PLtestlist0001", "Mix"
	e.fake.entries = []entry{{"a0000000000", "A", "Ch"}, {"p0000000000", "[Private video]", ""}, {"c0000000000", "C", "Ch"}}
	e.start(t)
	ctx := context.Background()
	res, err := e.svc.EnqueueURL(ctx, e.alice, listURL)
	if err != nil || len(res.Jobs) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, j := range res.Jobs {
		e.waitStatus(t, j.ID, StatusDone)
	}
	pid := res.Playlist.PlaylistID
	waitTitles(t, e, pid, []string{"A", "C"})

	// B is inserted between A and C on YouTube.
	e.fake.entries = []entry{{"a0000000000", "A", "Ch"}, {"b0000000000", "B", "Ch"}, {"c0000000000", "C", "Ch"}}
	again, err := e.svc.EnqueueURL(ctx, e.alice, listURL)
	if err != nil || again.Playlist.PlaylistID != pid || len(again.Jobs) != 3 {
		t.Fatalf("%+v %v", again, err)
	}
	e.waitStatus(t, again.Jobs[1].ID, StatusDone)
	waitTitles(t, e, pid, []string{"A", "B", "C"})
}

// A playlist the user already made with the list's exact name is adopted
// rather than duplicated — unless another list already owns it.
func TestListAdoptsSameNamedPlaylist(t *testing.T) {
	e := newEnv(t, true)
	e.fake.listID, e.fake.listTitle = "PLtestlist0001", "Road"
	e.fake.entries = []entry{{"a0000000000", "A", "Ch"}}
	mustExec(t, e, `INSERT INTO playlists(id,user_id,name,created_at,updated_at) VALUES (500,?,'Road',0,0)`, e.alice)
	res, err := e.svc.EnqueueURL(context.Background(), e.alice, listURL)
	if err != nil || res.Playlist.PlaylistID != 500 {
		t.Fatalf("%+v %v", res.Playlist, err)
	}
	e.fake.listID = "PLtestlist0002"
	other, err := e.svc.EnqueueURL(context.Background(), e.alice, "https://www.youtube.com/playlist?list=PLtestlist0002")
	if err != nil || other.Playlist.PlaylistID == 500 || other.Playlist.Name != "Road" {
		t.Fatalf("%+v %v", other.Playlist, err)
	}
}

func TestListEmptyTitle(t *testing.T) {
	e := newEnv(t, true)
	e.fake.listID = "PLtestlist0001"
	e.fake.entries = []entry{{"a0000000000", "A", "Ch"}}
	res, err := e.svc.EnqueueURL(context.Background(), e.alice, listURL)
	if err != nil || res.Playlist.Name != "YouTube PLtestlist0001" {
		t.Fatalf("%+v %v", res.Playlist, err)
	}
}

func TestSongInsideListOrMixIsNotAList(t *testing.T) {
	e := newEnv(t, true)
	e.fake.listID, e.fake.listTitle = "PLtestlist0001", "x"
	e.fake.entries = []entry{{"a0000000000", "A", "Ch"}}
	for _, u := range []string{"https://www.youtube.com/watch?v=a0000000000&list=PLtestlist0001", "https://www.youtube.com/watch?v=a0000000000&list=RDa0000000000"} {
		res, err := e.svc.EnqueueURL(context.Background(), e.alice, u)
		if err != nil || res.Playlist != nil || len(res.Jobs) != 1 {
			t.Fatalf("%s: %+v %v", u, res, err)
		}
	}
	// A mix page and a channel never become playlists either.
	e.fake.listID = "RDa0000000000"
	if res, err := e.svc.EnqueueURL(context.Background(), e.alice, "https://www.youtube.com/playlist?list=RDa0000000000"); err != nil || res.Playlist != nil {
		t.Fatalf("mix: %+v %v", res, err)
	}
	e.fake.listID = ""
	if res, err := e.svc.EnqueueURL(context.Background(), e.alice, "https://www.youtube.com/@somechannel"); err != nil || res.Playlist != nil {
		t.Fatalf("channel: %+v %v", res, err)
	}
	var n int
	e.store.DB.QueryRow(`SELECT COUNT(*) FROM download_lists`).Scan(&n)
	if n != 0 {
		t.Fatalf("download_lists rows: %d", n)
	}
}
