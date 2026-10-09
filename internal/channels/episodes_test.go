package channels

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestLatestFollowsSettingsAndUnplayed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.user("anna"), e.user("bo")
	e.follow(a, chA)
	e.follow(b, chA)
	if _, err := e.svc.UpdateSettings(ctx, b, chA, Settings{Media: "audio", IncludeShorts: true}); err != nil {
		t.Fatal(err)
	}
	e.done(chA, 1, time.Hour, 10)
	e.done(chA, 2, 2*time.Hour, 10)
	e.exec(`UPDATE episodes SET kind='short' WHERE video_id=?`, vid(2))
	e.seedQueued(chA, 3, 30*time.Minute, "audio")
	items, err := e.svc.Latest(ctx, a, 0, 50)
	if err != nil || len(items) != 2 || items[0].VideoID != vid(3) || items[1].VideoID != vid(1) {
		t.Fatalf("anna (no Shorts): %+v %v", items, err)
	}
	if items[0].Audio == nil || items[0].Audio.Status != "queued" || items[1].Audio.Status != "done" || items[1].Video != nil {
		t.Fatalf("%+v %+v", items[0].Audio, items[1])
	}
	if items[1].Thumbnail != "/api/v1/episodes/"+vid(1)+"/thumbnail" || items[1].ChannelTitle != "Channel aaaa" || items[1].Description != "" {
		t.Fatalf("%+v", items[1])
	}
	if got, _ := e.svc.Latest(ctx, b, 0, 50); len(got) != 3 {
		t.Fatalf("bo includes Shorts: %d", len(got))
	}
	if page, _ := e.svc.Latest(ctx, a, items[0].PublishedAt, 50); len(page) != 1 || page[0].VideoID != vid(1) {
		t.Fatalf("before= pages: %+v", page)
	}
	if n, _ := e.svc.Unplayed(ctx, a); n != 1 {
		t.Fatalf("unplayed %d (only downloaded episodes count)", n)
	}
	played := true
	if err := e.svc.SetProgress(ctx, a, vid(1), 0, &played); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.svc.Unplayed(ctx, a); n != 0 {
		t.Fatalf("after played: %d", n)
	}
	if n, _ := e.svc.Unplayed(ctx, b); n != 2 {
		t.Fatalf("bo's own count: %d", n)
	}
}

// Review focus 3: progress is per user and survives partial updates.
func TestProgressKeepHide(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b := e.user("anna"), e.user("bo")
	e.follow(a, chA)
	e.done(chA, 1, time.Hour, 10)
	if err := e.svc.SetProgress(ctx, a, vid(1), 1234.5, nil); err != nil {
		t.Fatal(err)
	}
	ep, err := e.svc.Episode(ctx, a, vid(1))
	if err != nil || ep.PositionS != 1234.5 || ep.Played || ep.Description != "" {
		t.Fatalf("%+v %v", ep, err)
	}
	played := true
	e.svc.SetProgress(ctx, a, vid(1), 1300, &played)
	e.svc.SetProgress(ctx, a, vid(1), 10, nil) // a later position report keeps "played"
	if ep, _ := e.svc.Episode(ctx, a, vid(1)); ep.PositionS != 10 || !ep.Played {
		t.Fatalf("%+v", ep)
	}
	if ep, _ := e.svc.Episode(ctx, b, vid(1)); ep.PositionS != 0 || ep.Played || ep.Kept {
		t.Fatalf("bo sees his own state: %+v", ep)
	}
	if err := e.svc.SetKeep(ctx, a, vid(1), true); err != nil {
		t.Fatal(err)
	}
	if kept, _ := e.svc.Kept(ctx, a); len(kept) != 1 || !kept[0].Kept {
		t.Fatalf("%+v", kept)
	}
	if kept, _ := e.svc.Kept(ctx, b); len(kept) != 0 {
		t.Fatal("keeps are per user")
	}
	if err := e.svc.SetHidden(ctx, a, vid(1), true); err != nil {
		t.Fatal(err)
	}
	if items, _ := e.svc.Latest(ctx, a, 0, 50); len(items) != 0 {
		t.Fatal("hidden for anna")
	}
	for _, err := range []error{e.svc.SetProgress(ctx, a, "nope", 1, nil), e.svc.SetKeep(ctx, a, vid(99), true)} {
		if !errors.Is(err, ErrBadID) && !errors.Is(err, ErrNotFound) {
			t.Fatalf("%v", err)
		}
	}
}

func TestKeepOfAnUndownloadedEpisodeQueuesIt(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user("anna")
	e.seedQueued(chA, 1, 60*24*time.Hour) // old, never downloaded
	e.seedQueued(chA, 2, time.Hour)
	e.exec(`INSERT INTO episode_files(video_id,kind,status,created_at,updated_at) VALUES (?,'audio','expired',1,1)`, vid(2))
	e.svc.SetKeep(ctx, a, vid(1), true)
	e.svc.SetKeep(ctx, a, vid(2), true)
	if e.fileStatus(vid(1), "audio") != "queued" || e.fileStatus(vid(2), "audio") != "queued" {
		t.Fatal("keeping something that isn't on disk fetches it")
	}
}

func TestMediaFileStates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.done(chA, 1, time.Hour, 10)
	e.seedQueued(chA, 2, time.Hour, "audio")
	p, err := e.svc.MediaFile(ctx, vid(1), "audio")
	if err != nil || p != e.path(chA, vid(1)+".m4a") {
		t.Fatalf("%q %v", p, err)
	}
	if _, err := e.svc.MediaFile(ctx, vid(2), "audio"); !errors.Is(err, ErrNotReady) {
		t.Fatal(err)
	}
	if _, err := e.svc.MediaFile(ctx, vid(1), "video"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	e.svc.expire(ctx, vid(1), chA)
	if _, err := e.svc.MediaFile(ctx, vid(1), "audio"); !errors.Is(err, ErrExpired) {
		t.Fatal(err)
	}
	if _, ok := e.svc.ThumbnailPath(ctx, vid(1)); ok {
		t.Fatal("the thumbnail went with the files")
	}
}

func TestChannelPageOfAnUnfollowedChannelReadsItsFeed(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user("anna")
	if err := e.svc.EnsureChannel(ctx, ytdlpChannel(chB, "B")); err != nil {
		t.Fatal(err)
	}
	e.feeds.feeds[chB] = Feed{Entries: []FeedEntry{e.entry(1, time.Hour), e.entry(2, 2*time.Hour)}}
	page, err := e.svc.ChannelPage(ctx, a, chB)
	if err != nil || page.Following != nil || page.Followers != 0 || len(page.Episodes) != 2 || page.Channel.Title != "B" {
		t.Fatalf("%+v %v", page, err)
	}
	page, _ = e.svc.ChannelPage(ctx, a, chB)
	if len(e.feeds.calls) != 1 {
		t.Fatalf("read again within 30 min: %v", e.feeds.calls)
	}
	if _, err := e.svc.ChannelPage(ctx, a, chA); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	e.follow(a, chB)
	if page, _ := e.svc.ChannelPage(ctx, a, chB); page.Following == nil || page.Followers != 1 {
		t.Fatalf("%+v", page)
	}
}

// Streaming is confined to channels.root: a stored path that climbs out is not served.
func TestMediaFileStaysInsideRoot(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.seedQueued(chA, 1, time.Hour, "audio")
	for _, rel := range []string{"../escape.m4a", "../../etc/passwd", ""} {
		e.exec(`UPDATE episode_files SET status='done', path=? WHERE video_id=?`, rel, vid(1))
		if p, err := e.svc.MediaFile(ctx, vid(1), "audio"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("%q → %q %v", rel, p, err)
		}
	}
}

// Keeping an expired episode fetches its audio from scratch: no spent
// attempts, own timeouts or 403s carried over.
func TestKeepRequeuesExpiredFromScratch(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.user("anna")
	e.seedQueued(chA, 1, time.Hour, "audio")
	e.exec(`UPDATE episode_files SET status='expired', attempts=2, timeouts=1, transients=5, error='x', next_attempt_at=99`)
	if err := e.svc.SetKeep(ctx, a, vid(1), true); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT COUNT(*) FROM episode_files WHERE status='queued' AND attempts=0 AND timeouts=0 AND transients=0 AND error='' AND next_attempt_at=0`) != 1 {
		t.Fatal("a kept expired file is queued from scratch")
	}
}
