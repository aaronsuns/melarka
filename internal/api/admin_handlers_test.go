package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every admin route must refuse members. Hiding UI is not access control.
func TestAdminRoutesForbiddenForMembers(t *testing.T) {
	s, ts := newTestServer(t)
	kid := loginAs(t, s, "kid", "member")
	id := seedTrack(t, s, "a.mp3", "A", "X", "Y")
	routes := [][2]string{
		{"DELETE", fmt.Sprintf("/api/v1/tracks/%d", id)},
		{"PUT", fmt.Sprintf("/api/v1/tracks/%d/status", id)},
		{"PATCH", fmt.Sprintf("/api/v1/tracks/%d", id)},
		{"PUT", fmt.Sprintf("/api/v1/tracks/%d/tags", id)},
		{"GET", fmt.Sprintf("/api/v1/tracks/%d/lyrics/candidates", id)}, {"PUT", fmt.Sprintf("/api/v1/tracks/%d/lyrics", id)},
		{"POST", fmt.Sprintf("/api/v1/tracks/%d/lyrics/refresh", id)},
		{"DELETE", fmt.Sprintf("/api/v1/tracks/%d/lyrics/candidates/1", id)},
		{"GET", fmt.Sprintf("/api/v1/tracks/%d/lyrics/rejected", id)}, {"DELETE", fmt.Sprintf("/api/v1/tracks/%d/lyrics/rejected/1", id)},
		{"GET", "/api/v1/trash"}, {"POST", "/api/v1/trash/1/restore"}, {"DELETE", "/api/v1/trash"},
		{"GET", "/api/v1/admin/broken-tracks"}, {"DELETE", "/api/v1/admin/broken-tracks"},
		{"GET", "/api/v1/users"}, {"POST", "/api/v1/users"}, {"DELETE", "/api/v1/users/1"}, {"PUT", "/api/v1/users/1/password"},
		{"GET", "/api/v1/admin/libraries"}, {"POST", "/api/v1/admin/libraries"}, {"DELETE", "/api/v1/admin/libraries/1"},
		{"POST", "/api/v1/admin/scan"}, {"GET", "/api/v1/admin/scan/status"}, {"GET", "/api/v1/admin/tagging/pending"}, {"PUT", "/api/v1/admin/tagging/batch"},
		{"GET", "/api/v1/admin/ytdlp"}, {"POST", "/api/v1/admin/ytdlp/update"},
		{"GET", "/api/v1/admin/lyrics/missing"}, {"GET", "/api/v1/admin/lyrics/missing/count"},
		{"GET", "/api/v1/admin/metadata/review"}, {"GET", "/api/v1/admin/metadata/review/count"},
		{"PUT", fmt.Sprintf("/api/v1/admin/metadata/review/%d", id)}, {"GET", fmt.Sprintf("/api/v1/admin/metadata/review/%d", id)},
	}
	for _, r := range routes {
		resp, _ := do(t, ts, kid, r[0], r[1], map[string]any{})
		if resp.StatusCode != 403 {
			t.Errorf("%s %s → %d, want 403", r[0], r[1], resp.StatusCode)
		}
	}
	if resp, _ := do(t, ts, kid, "GET", fmt.Sprintf("/api/v1/tracks/%d/tags", id), nil); resp.StatusCode != 200 {
		t.Errorf("member tag read %d", resp.StatusCode)
	}
	if resp, _ := do(t, ts, kid, "GET", fmt.Sprintf("/api/v1/tracks/%d/lyrics", id), nil); resp.StatusCode != 200 {
		t.Errorf("member lyrics read %d", resp.StatusCode)
	}
	// Members may shift and report lyrics (no lyrics here yet: 409, never 403).
	if resp, _ := do(t, ts, kid, "PUT", fmt.Sprintf("/api/v1/tracks/%d/lyrics/offset", id), map[string]int{"offset_ms": 100}); resp.StatusCode != 409 {
		t.Errorf("member offset → %d, want 409", resp.StatusCode)
	}
	if resp, _ := do(t, ts, kid, "POST", fmt.Sprintf("/api/v1/tracks/%d/lyrics/wrong", id), map[string]int{"lyrics_id": 1}); resp.StatusCode != 409 {
		t.Errorf("member wrong → %d, want 409", resp.StatusCode)
	}
	if resp, _ := do(t, ts, kid, "POST", fmt.Sprintf("/api/v1/tracks/%d/lyrics/wrong/undo", id), map[string]int{"report_id": 1}); resp.StatusCode != 404 {
		t.Errorf("member undo of an unknown report → %d, want 404", resp.StatusCode)
	}
}

func TestAdminTrackEditing(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	id := seedTrack(t, s, "a.mp3", "A", "X", "Y")
	p := fmt.Sprintf("/api/v1/tracks/%d", id)
	if r, b := do(t, ts, adm, "PATCH", p, map[string]any{"title": "新名字"}); r.StatusCode != 204 {
		t.Fatalf("patch %d %s", r.StatusCode, b)
	}
	if _, b := do(t, ts, adm, "GET", p, nil); !strings.Contains(string(b), "新名字") {
		t.Fatalf("after patch %s", b)
	}
	if r, _ := do(t, ts, adm, "PUT", p+"/status", map[string]string{"status": "pending"}); r.StatusCode != 204 {
		t.Fatalf("status %d", r.StatusCode)
	}
	if r, _ := do(t, ts, adm, "PUT", p+"/status", map[string]string{"status": "trashed"}); r.StatusCode != 400 {
		t.Fatalf("status trashed %d", r.StatusCode)
	}
	r, b := do(t, ts, adm, "PUT", p+"/tags", map[string]any{"source": "manual", "tags": []map[string]string{{"name": "开车", "kind": "scene"}}})
	if r.StatusCode != 204 {
		t.Fatalf("tags %d %s", r.StatusCode, b)
	}
	if _, b := do(t, ts, adm, "GET", p+"/tags", nil); !strings.Contains(string(b), "开车") {
		t.Fatalf("tags read %s", b)
	}
}

// A trashed track's rel_path points into ".lark-trash/…"; SetOverrides and
// the tag PUT must refuse it (404) rather than Reindex it, which would
// re-add that trash path to the search index.
func TestAdminRefusesEditsOnTrashedTrack(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	id := seedTrack(t, s, "trashme.mp3", "A", "X", "Y")
	if _, err := s.Library.DB.Exec(`UPDATE tracks SET status='trashed' WHERE id=?`, id); err != nil {
		t.Fatal(err)
	}
	p := fmt.Sprintf("/api/v1/tracks/%d", id)
	if r, b := do(t, ts, adm, "PATCH", p, map[string]any{"title": "should not apply"}); r.StatusCode != 404 {
		t.Fatalf("patch trashed track: %d %s", r.StatusCode, b)
	}
	if r, b := do(t, ts, adm, "PUT", p+"/tags", map[string]any{"source": "manual", "tags": []map[string]string{{"name": "开车", "kind": "scene"}}}); r.StatusCode != 404 {
		t.Fatalf("tags on trashed track: %d %s", r.StatusCode, b)
	}
}

// addLibrary must confine roots to under Server.MusicRoot: os.Stat alone only
// proves a path is *a* directory, not a safe one — "/", "/etc" or the data
// dir would otherwise be accepted, and trash.Service would later create
// "<root>/.lark-trash" right next to whatever lives there.
func TestAddLibraryConfinedToMusicRoot(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	root := t.TempDir()
	s.MusicRoot = root

	bad := map[string]string{
		"root slash":         "/",
		"outside root":       "/etc",
		"root itself":        root,
		"relative path":      "music/foo",
		"escapes via dotdot": filepath.Join(root, "..", "etc"),
	}
	for name, p := range bad {
		if r, b := do(t, ts, adm, "POST", "/api/v1/admin/libraries", map[string]any{"name": name, "root": p}); r.StatusCode != 400 {
			t.Errorf("%s (root=%q): got %d %s, want 400", name, p, r.StatusCode, b)
		}
	}

	good := filepath.Join(root, "sub")
	if err := os.MkdirAll(good, 0o755); err != nil {
		t.Fatal(err)
	}
	if r, b := do(t, ts, adm, "POST", "/api/v1/admin/libraries", map[string]any{"name": "good", "root": good}); r.StatusCode != 201 {
		t.Fatalf("valid root under MusicRoot: got %d %s, want 201", r.StatusCode, b)
	}
}

// POST /admin/libraries with a name that already exists
// used to fall straight into EnsureLibrary's ON CONFLICT(name) upsert, which
// silently repointed the existing library's root at whatever path this
// request happened to supply. It must instead be refused (409) and leave
// the existing library's root untouched.
func TestAddLibraryDuplicateNameConflicts(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	root := t.TempDir()
	s.MusicRoot = root

	first := filepath.Join(root, "first")
	if err := os.MkdirAll(first, 0o755); err != nil {
		t.Fatal(err)
	}
	if r, b := do(t, ts, adm, "POST", "/api/v1/admin/libraries", map[string]any{"name": "main", "root": first}); r.StatusCode != 201 {
		t.Fatalf("first create: got %d %s, want 201", r.StatusCode, b)
	}

	second := filepath.Join(root, "second")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	r, b := do(t, ts, adm, "POST", "/api/v1/admin/libraries", map[string]any{"name": "main", "root": second})
	if r.StatusCode != 409 {
		t.Fatalf("duplicate name: got %d %s, want 409", r.StatusCode, b)
	}

	_, body := do(t, ts, adm, "GET", "/api/v1/admin/libraries", nil)
	if !strings.Contains(string(body), first) {
		t.Fatalf("existing library's root was repointed: %s", body)
	}
	if strings.Contains(string(body), second) {
		t.Fatalf("existing library's root was repointed to the duplicate request's root: %s", body)
	}
}

// createUser and putTrackTags must map validation-shaped store errors to 400
// via recognized sentinels (auth.ErrInvalid, tags.ErrUnknownSource) instead
// of forwarding arbitrary err.Error() text — anything else goes through
// s.fail (500, logged) rather than leaking as a 400.
func TestCreateUserValidationIsA400(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	if r, b := do(t, ts, adm, "POST", "/api/v1/users", map[string]string{"username": "norole", "password": "pw"}); r.StatusCode != 400 {
		t.Fatalf("missing role: got %d %s, want 400", r.StatusCode, b)
	}
}

func TestPutTrackTagsUnknownSourceIsA400(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	id := seedTrack(t, s, "b.mp3", "A", "X", "Y")
	p := fmt.Sprintf("/api/v1/tracks/%d/tags", id)
	body := map[string]any{"source": "bogus", "tags": []map[string]string{{"name": "x", "kind": "genre"}}}
	if r, b := do(t, ts, adm, "PUT", p, body); r.StatusCode != 400 {
		t.Fatalf("unknown source: got %d %s, want 400", r.StatusCode, b)
	}
}

// writeScript writes a small shell script to path, executable, so it can
// stand in for "the image's yt-dlp binary" in tests without needing a real
// yt-dlp anywhere — copyYtDlpBinary/exec.LookPath treat any executable file
// the same way.
func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

// copyYtDlpBinary must replace dst atomically (via temp file + rename): the
// destination always has fully-written, correctly-moded content, whether
// dst is missing (first update) or already exists (a later one).
func TestCopyYtDlpBinaryAtomic(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src", "yt-dlp")
	writeScript(t, src, `echo v1`)
	dst := filepath.Join(dir, "bin", "yt-dlp") // parent doesn't exist yet

	if err := copyYtDlpBinary(src, dst); err != nil {
		t.Fatalf("first copy: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "echo v1") {
		t.Fatalf("dst content: %s", got)
	}
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode()&0o111 == 0 {
		t.Fatalf("dst not executable: %v", st.Mode())
	}

	// Second copy, from a different source, must fully replace dst (rename
	// over it) rather than appending or leaving stale bytes.
	writeScript(t, src, `echo v2`)
	if err := copyYtDlpBinary(src, dst); err != nil {
		t.Fatalf("second copy: %v", err)
	}
	got, err = os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), "v1") || !strings.Contains(string(got), "echo v2") {
		t.Fatalf("dst not fully replaced: %s", got)
	}

	// No temp file left behind in dst's directory.
	entries, err := os.ReadDir(filepath.Dir(dst))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".yt-dlp-") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

// A successful update copies the source script into the data dir, and both
// the "-U" run and the post-copy verification run the real (fake, here)
// script — GET-equivalent info in the response reflects the data-dir copy.
func TestYtdlpUpdateCopiesAndVerifies(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")

	src := filepath.Join(t.TempDir(), "image-yt-dlp")
	writeScript(t, src, `echo 9.9.9`)
	s.YtDlpPath = src
	s.YtDlpBin = filepath.Join(t.TempDir(), "bin", "yt-dlp")

	r, b := do(t, ts, adm, "POST", "/api/v1/admin/ytdlp/update", nil)
	if r.StatusCode != 200 {
		t.Fatalf("update: %d %s", r.StatusCode, b)
	}
	var out struct{ Version, Path string }
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Version != "9.9.9" {
		t.Fatalf("version: got %q", out.Version)
	}
	if out.Path != s.YtDlpBin {
		t.Fatalf("path: got %q, want data-dir copy %q", out.Path, s.YtDlpBin)
	}
	if _, err := os.Stat(s.YtDlpBin); err != nil {
		t.Fatalf("data-dir copy missing: %v", err)
	}
}

// If the freshly-updated data-dir copy can't actually run (wrong arch, a
// corrupt fetch, ...), the handler must revert: remove the copy so every
// future Bin() resolution falls back to the configured path instead of
// silently shadowing a working binary forever, and report that fallback.
func TestYtdlpUpdateRevertsWhenCopyIsBroken(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	s.YT.Runner.(*fakeYTRunner).out = []byte("2026.07.04\n") // the "-U" run + fallback Version()

	src := filepath.Join(t.TempDir(), "image-yt-dlp")
	writeScript(t, src, `exit 7`) // "copied but doesn't run" stand-in
	s.YtDlpPath = src
	s.YtDlpBin = filepath.Join(t.TempDir(), "bin", "yt-dlp")

	r, b := do(t, ts, adm, "POST", "/api/v1/admin/ytdlp/update", nil)
	if r.StatusCode != 200 {
		t.Fatalf("update: %d %s", r.StatusCode, b)
	}
	if _, err := os.Stat(s.YtDlpBin); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("broken copy was not removed: err=%v", err)
	}
	var out struct{ Version, Path string }
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Path != s.YtDlpPath {
		t.Fatalf("path: got %q, want fallback %q after revert", out.Path, s.YtDlpPath)
	}
	if out.Version != "2026.07.04" {
		t.Fatalf("version: got %q", out.Version)
	}
}

// GET /admin/ytdlp must report the version the running binary resolves to
// (here, the fake runner standing in for it) and the configured fallback
// path, since the data-dir copy doesn't exist in a test environment.
func TestYtdlpInfoForAdmin(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	s.YT.Runner.(*fakeYTRunner).out = []byte("2026.07.04\n")

	r, b := do(t, ts, adm, "GET", "/api/v1/admin/ytdlp", nil)
	if r.StatusCode != 200 {
		t.Fatalf("ytdlp info: %d %s", r.StatusCode, b)
	}
	var info struct{ Version, Path string }
	if err := json.Unmarshal(b, &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if info.Version != "2026.07.04" {
		t.Fatalf("version: got %q", info.Version)
	}
	if info.Path != s.YtDlpPath {
		t.Fatalf("path: got %q, want fallback %q (data-dir copy doesn't exist)", info.Path, s.YtDlpPath)
	}
}

func TestUserManagement(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	r, b := do(t, ts, adm, "POST", "/api/v1/users", map[string]string{"username": "bob", "password": "pw", "role": "member"})
	if r.StatusCode != 201 {
		t.Fatalf("create %d %s", r.StatusCode, b)
	}
	if r, _ := do(t, ts, adm, "POST", "/api/v1/users", map[string]string{"username": "bob", "password": "pw", "role": "member"}); r.StatusCode != 409 {
		t.Fatalf("dup %d", r.StatusCode)
	}
	_, b = do(t, ts, adm, "GET", "/api/v1/me", nil)
	var me struct{ ID int64 }
	json.Unmarshal(b, &me)
	if r, _ := do(t, ts, adm, "DELETE", fmt.Sprintf("/api/v1/users/%d", me.ID), nil); r.StatusCode != 400 {
		t.Fatalf("self delete %d", r.StatusCode)
	}
}

// 全部删除 on the admin console's damaged files: every broken track goes to
// the trash (restorable, as one deleted by itself), the count comes back,
// and a second call finds nothing left.
func TestTrashBrokenTracks(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	kid := loginAs(t, s, "kid", "member")
	root := t.TempDir()
	lib, err := s.Library.EnsureLibrary(context.Background(), "main", root, false)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, rel := range []string{"bad1.mp3", "bad2.flac", "good.mp3"} {
		if err := os.WriteFile(filepath.Join(root, rel), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, seedTrackIn(t, s, lib.ID, rel, rel, "A"))
	}
	s.Library.DB.Exec(`UPDATE tracks SET broken=1, broken_reason='Invalid data' WHERE id IN (?,?)`, ids[0], ids[1])

	if r, _ := do(t, ts, kid, "DELETE", "/api/v1/admin/broken-tracks", nil); r.StatusCode != 403 {
		t.Fatalf("member → %d, want 403", r.StatusCode)
	}
	// The admin disliking one doesn't hide it: the list is what DELETE moves.
	if r, _ := do(t, ts, adm, "PUT", fmt.Sprintf("/api/v1/dislikes/%d", ids[1]), nil); r.StatusCode != 204 {
		t.Fatalf("dislike %d", r.StatusCode)
	}
	if _, b := do(t, ts, adm, "GET", "/api/v1/tracks?broken=1", nil); strings.Contains(string(b), "bad2") {
		t.Fatalf("precondition: the general list hides a dislike %s", b)
	}
	_, b := do(t, ts, adm, "GET", "/api/v1/admin/broken-tracks", nil)
	var list struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		Total int `json:"total"`
	}
	json.Unmarshal(b, &list)
	if list.Total != 2 || len(list.Items) != 2 {
		t.Fatalf("broken list %s", b)
	}
	// A different count than the one confirmed moves nothing.
	if r, b := do(t, ts, adm, "DELETE", "/api/v1/admin/broken-tracks?expect=1", nil); r.StatusCode != 409 || !strings.Contains(string(b), `"broken_count_changed"`) {
		t.Fatalf("expect=1 → %d %s", r.StatusCode, b)
	}
	if r, _ := do(t, ts, adm, "DELETE", "/api/v1/admin/broken-tracks?expect=x", nil); r.StatusCode != 400 {
		t.Fatalf("expect=x → %d", r.StatusCode)
	}
	if _, err := os.Stat(filepath.Join(root, "bad1.mp3")); err != nil {
		t.Fatal("moved on a refused call")
	}
	r, b := do(t, ts, adm, "DELETE", "/api/v1/admin/broken-tracks?expect=2", nil)
	if r.StatusCode != 200 || strings.TrimSpace(string(b)) != `{"trashed":2}` {
		t.Fatalf("trash broken %d %s", r.StatusCode, b)
	}
	_, b = do(t, ts, adm, "GET", "/api/v1/trash", nil)
	var items []struct {
		TrackID int64 `json:"track_id"`
		PurgeAt int64 `json:"purge_at"`
	}
	json.Unmarshal(b, &items)
	if len(items) != 2 || items[0].PurgeAt == 0 {
		t.Fatalf("trash %s", b)
	}
	if _, b := do(t, ts, adm, "GET", "/api/v1/tracks?broken=1", nil); strings.Contains(string(b), "bad") {
		t.Fatalf("still listed as damaged %s", b)
	}
	if _, err := os.Stat(filepath.Join(root, "good.mp3")); err != nil {
		t.Fatal("a sound file was moved")
	}
	// Idempotent.
	if r, b := do(t, ts, adm, "DELETE", "/api/v1/admin/broken-tracks", nil); r.StatusCode != 200 || strings.TrimSpace(string(b)) != `{"trashed":0}` {
		t.Fatalf("again %d %s", r.StatusCode, b)
	}
	// Restorable like a track deleted by itself.
	if r, b := do(t, ts, adm, "POST", fmt.Sprintf("/api/v1/trash/%d/restore", ids[0]), nil); r.StatusCode != 204 {
		t.Fatalf("restore %d %s", r.StatusCode, b)
	}
	if _, err := os.Stat(filepath.Join(root, "bad1.mp3")); err != nil {
		t.Fatal("not restored")
	}
}

// A damaged file that can't be moved (gone from disk since the scan) doesn't
// stop the rest, and the answer says so with a stable code.
func TestTrashBrokenTracksReportsFailures(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	root := t.TempDir()
	lib, _ := s.Library.EnsureLibrary(context.Background(), "main", root, false)
	os.WriteFile(filepath.Join(root, "bad.mp3"), []byte("x"), 0o644)
	gone := seedTrackIn(t, s, lib.ID, "vanished.mp3", "v", "A")
	bad := seedTrackIn(t, s, lib.ID, "bad.mp3", "b", "A")
	s.Library.DB.Exec(`UPDATE tracks SET broken=1 WHERE id IN (?,?)`, gone, bad)
	r, b := do(t, ts, adm, "DELETE", "/api/v1/admin/broken-tracks", nil)
	var e struct{ Error, Code string }
	json.Unmarshal(b, &e)
	if r.StatusCode != 500 || e.Code != "broken_trash_incomplete" || !strings.Contains(e.Error, "1 of 2") {
		t.Fatalf("partial %d %s", r.StatusCode, b)
	}
	var st string
	s.Library.DB.QueryRow(`SELECT status FROM tracks WHERE id=?`, bad).Scan(&st)
	if st != "trashed" {
		t.Fatalf("the movable one: status=%s", st)
	}
}

// The broken list stops at 500 rows, but its total is every broken track.
func TestBrokenTracksTotalBeyondThePage(t *testing.T) {
	s, ts := newTestServer(t)
	adm := loginAs(t, s, "dad", "admin")
	lib, _ := s.Library.EnsureLibrary(context.Background(), "main", t.TempDir(), false)
	for i := 0; i < 502; i++ {
		rel := fmt.Sprintf("b%03d.mp3", i)
		if _, err := s.Library.DB.Exec(`INSERT INTO tracks(library_id,rel_path,size,mtime,fingerprint,status,added_at,broken) VALUES (?,?,1,1,?,'kept',1,1)`, lib.ID, rel, rel); err != nil {
			t.Fatal(err)
		}
	}
	seedTrackIn(t, s, lib.ID, "fine.mp3", "fine", "A")
	_, b := do(t, ts, adm, "GET", "/api/v1/admin/broken-tracks", nil)
	var list struct {
		Items []json.RawMessage `json:"items"`
		Total int               `json:"total"`
	}
	if err := json.Unmarshal(b, &list); err != nil {
		t.Fatal(err)
	}
	if list.Total != 502 || len(list.Items) != 500 {
		t.Fatalf("total=%d items=%d", list.Total, len(list.Items))
	}
}
