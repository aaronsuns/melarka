package channels

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/aaronsuns/lark-server/internal/ytdlp"
)

func queued(e *env) []string {
	e.t.Helper()
	rows, err := e.db.Query(`SELECT video_id||'/'||kind FROM episode_files WHERE status='queued' ORDER BY video_id, kind`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func TestFirstPollBackfillsTheNewestThree(t *testing.T) {
	e := newEnv(t)
	u := e.user("anna")
	e.follow(u, chA)
	short := e.entry(9, 30*time.Minute)
	short.Short = true
	e.feeds.feeds[chA] = Feed{Title: "A", Entries: []FeedEntry{short, e.entry(1, time.Hour), e.entry(2, 2*time.Hour),
		e.entry(3, 3*time.Hour), e.entry(4, 4*time.Hour), e.entry(5, 5*time.Hour)}}
	if err := e.svc.PollChannel(context.Background(), chA); err != nil {
		t.Fatal(err)
	}
	if got := queued(e); !slices.Equal(got, []string{vid(1) + "/audio", vid(2) + "/audio", vid(3) + "/audio"}) {
		t.Fatalf("queued %v", got)
	}
	if slices.Contains(e.yt.infoCalls, vid(9)) {
		t.Fatal("a Short (from its /shorts/ link) needs no yt-dlp read")
	}
	if e.count(`SELECT backfill_pending FROM channel_follows`) != 0 {
		t.Fatal("backfill must happen once")
	}
	var polled, next int64
	e.db.QueryRow(`SELECT polled_at, next_poll_at FROM channels WHERE id=?`, chA).Scan(&polled, &next)
	if polled != t0.Unix() || next < t0.Add(2*time.Hour).Unix() || next >= t0.Add(2*time.Hour+5*time.Minute).Unix() {
		t.Fatalf("polled %d next %d", polled, next)
	}
}

func TestNewEpisodesFollowEachFollowersSettings(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, b, c := e.user("anna"), e.user("bo"), e.user("cy")
	e.follow(a, chA)
	e.follow(b, chA)
	e.follow(c, chA)
	if _, err := e.svc.UpdateSettings(ctx, b, chA, Settings{Media: "video"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.UpdateSettings(ctx, c, chA, Settings{Media: "audio", IncludeShorts: true}); err != nil {
		t.Fatal(err)
	}
	e.svc.InitialBackfill = 0
	e.feeds.feeds[chA] = Feed{Entries: []FeedEntry{e.entry(1, time.Hour)}}
	e.svc.PollChannel(ctx, chA)
	if got := queued(e); len(got) != 0 {
		t.Fatalf("nothing new since following: %v", got)
	}
	e.clock.Add(3 * time.Hour)
	short := e.entry(3, time.Hour)
	short.Short = true
	e.feeds.feeds[chA] = Feed{Entries: []FeedEntry{short, e.entry(2, 2*time.Hour), e.entry(1, 4*time.Hour)}}
	e.svc.PollChannel(ctx, chA)
	want := []string{vid(2) + "/audio", vid(2) + "/video", vid(3) + "/audio"}
	if got := queued(e); !slices.Equal(got, want) {
		t.Fatalf("queued %v, want %v (video because bo wants it; the Short because cy includes Shorts)", got, want)
	}
}

func TestLiveUpcomingReplayAndUnavailable(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	u := e.user("anna")
	e.follow(u, chA)
	e.svc.InitialBackfill = 0
	e.clock.Add(time.Hour)
	e.yt.infos[vid(1)] = ytdlp.Info{ID: vid(1), LiveStatus: "is_upcoming", Availability: "public"}
	e.yt.infos[vid(2)] = ytdlp.Info{ID: vid(2), LiveStatus: "is_live", Availability: "public"}
	e.yt.infoErr[vid(3)] = errors.New("ERROR: [youtube] x: Join this channel to get access to members-only content like this video")
	e.yt.infos[vid(4)] = ytdlp.Info{ID: vid(4), LiveStatus: "not_live", Availability: "subscriber_only"}
	e.feeds.feeds[chA] = Feed{Entries: []FeedEntry{e.entry(1, time.Minute), e.entry(2, 2*time.Minute), e.entry(3, 3*time.Minute), e.entry(4, 4*time.Minute)}}
	e.svc.PollChannel(ctx, chA)
	kinds := map[string]string{}
	rows, _ := e.db.Query(`SELECT video_id, kind FROM episodes`)
	for rows.Next() {
		var v, k string
		rows.Scan(&v, &k)
		kinds[v] = k
	}
	rows.Close()
	if kinds[vid(1)] != "upcoming" || kinds[vid(2)] != "live" || kinds[vid(3)] != "unavailable" || kinds[vid(4)] != "unavailable" {
		t.Fatalf("%v", kinds)
	}
	if got := queued(e); len(got) != 0 {
		t.Fatalf("%v", got)
	}
	// Next poll: the premiere is over (a replay), the stream still live. Replays only for include_live.
	e.yt.infos[vid(1)] = ytdlp.Info{ID: vid(1), LiveStatus: "was_live", Availability: "public", DurationS: 3600}
	e.yt.infoCalls = nil
	e.svc.PollChannel(ctx, chA)
	if slices.Contains(e.yt.infoCalls, vid(3)) || slices.Contains(e.yt.infoCalls, vid(4)) {
		t.Fatalf("unavailable episodes are never read again: %v", e.yt.infoCalls)
	}
	if got := queued(e); len(got) != 0 {
		t.Fatalf("replays are off by default: %v", got)
	}
	if _, err := e.svc.UpdateSettings(ctx, u, chA, Settings{Media: "audio", IncludeLive: true}); err != nil {
		t.Fatal(err)
	}
	e.svc.PollChannel(ctx, chA)
	if got := queued(e); !slices.Equal(got, []string{vid(1) + "/audio"}) {
		t.Fatalf("%v", got)
	}
}

// A bot check stops this poll's detail reads and marks nothing.
func TestBotCheckStopsDetailReadsWithoutMarkingAnything(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.follow(e.user("anna"), chA)
	bot := errors.New("ERROR: [youtube] x: Sign in to confirm you're not a bot. Use --cookies-from-browser")
	for i := 1; i <= 4; i++ {
		e.yt.infoErr[vid(i)] = bot
	}
	e.feeds.feeds[chA] = Feed{Entries: []FeedEntry{e.entry(1, time.Hour), e.entry(2, 2*time.Hour), e.entry(3, 3*time.Hour), e.entry(4, 4*time.Hour)}}
	if err := e.svc.PollChannel(ctx, chA); err != nil {
		t.Fatalf("the feed itself was fine: %v", err)
	}
	if len(e.yt.infoCalls) != 1 {
		t.Fatalf("one failed read is enough for this poll: %v", e.yt.infoCalls)
	}
	if e.count(`SELECT COUNT(*) FROM episodes WHERE kind='unknown'`) != 4 || len(queued(e)) != 0 || e.count(`SELECT backfill_pending FROM channel_follows`) != 1 {
		t.Fatal("nothing may be marked, queued or considered backfilled")
	}
	e.yt.infoErr = map[string]error{}
	e.svc.PollChannel(ctx, chA)
	if got := queued(e); len(got) != 3 {
		t.Fatalf("next poll backfills: %v", got)
	}
}

// Feed failures back off per channel, capped at 24 h, and recover.
func TestPollFailureBacksOff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.follow(e.user("anna"), chA)
	e.feeds.errs[chA] = errors.New("channels: feed answered 500")
	for i, want := range []time.Duration{2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour} {
		if err := e.svc.PollChannel(ctx, chA); err == nil {
			t.Fatal("want an error")
		}
		var next, fails int64
		var last string
		e.db.QueryRow(`SELECT next_poll_at, poll_failures, last_error FROM channels WHERE id=?`, chA).Scan(&next, &fails, &last)
		if next != e.clock.Now().Add(want).Unix() || fails != int64(i+1) || last != "channels: feed answered 500" {
			t.Fatalf("failure %d: next +%ds fails %d %q", i+1, next-e.clock.Now().Unix(), fails, last)
		}
	}
	delete(e.feeds.errs, chA)
	e.feeds.feeds[chA] = Feed{Entries: []FeedEntry{}}
	if err := e.svc.PollChannel(ctx, chA); err != nil {
		t.Fatal(err)
	}
	if e.count(`SELECT poll_failures FROM channels`) != 0 || e.count(`SELECT COUNT(*) FROM channels WHERE last_error=''`) != 1 {
		t.Fatal("success resets the backoff")
	}
}

func TestPollStepSchedule(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if w := e.svc.pollStep(ctx); w != pollIdleMax {
		t.Fatalf("nothing followed: %s", w)
	}
	u := e.user("anna")
	e.follow(u, chA)
	e.feeds.feeds[chA] = Feed{Entries: []FeedEntry{}}
	e.svc.pollStep(ctx)
	if len(e.feeds.calls) != 1 {
		t.Fatalf("a new follow is polled at once: %v", e.feeds.calls)
	}
	if w := e.svc.pollStep(ctx); w <= 0 || w > pollIdleMax || len(e.feeds.calls) != 1 {
		t.Fatalf("not due again yet: %s %v", w, e.feeds.calls)
	}
	if _, err := e.svc.UpdateSettings(ctx, u, chA, Settings{Media: "audio", Paused: true}); err != nil {
		t.Fatal(err)
	}
	e.clock.Add(3 * time.Hour)
	if w := e.svc.pollStep(ctx); w != pollIdleMax || len(e.feeds.calls) != 1 {
		t.Fatalf("only paused followers: no polling (%s, %v)", w, e.feeds.calls)
	}
}

// A video whose details never read (a premiere yt-dlp keeps
// failing on) never blocks the others' classification or the backfill, and
// after maxInfoFailures failed reads it is marked unavailable and skipped.
func TestFailingDetailReadNeverBlocksTheOthers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.follow(e.user("anna"), chA)
	e.yt.infoErr[vid(1)] = errors.New("yt-dlp: exit status 1: ERROR: [youtube] x: Premieres in 2 hours")
	e.feeds.feeds[chA] = Feed{Entries: []FeedEntry{e.entry(1, time.Hour), e.entry(2, 2*time.Hour), e.entry(3, 3*time.Hour),
		e.entry(4, 4*time.Hour), e.entry(5, 5*time.Hour)}}
	if err := e.svc.PollChannel(ctx, chA); err != nil {
		t.Fatal(err)
	}
	if got := queued(e); !slices.Equal(got, []string{vid(2) + "/audio", vid(3) + "/audio", vid(4) + "/audio"}) {
		t.Fatalf("the others are classified and backfilled: %v", got)
	}
	if e.count(`SELECT backfill_pending FROM channel_follows`) != 0 {
		t.Fatal("backfill done despite the failing premiere")
	}
	// A new premiere (wanted by every poll, unlike the backfill's one) that
	// keeps failing: read once per poll until it is given up on.
	e.clock.Add(3 * time.Hour)
	// Strikes only count in a poll where other reads succeed: a live stream
	// (read again every poll) is that success here.
	e.yt.infoErr[vid(6)] = e.yt.infoErr[vid(1)]
	e.yt.infos[vid(7)] = ytdlp.Info{ID: vid(7), LiveStatus: "is_live", Availability: "public"}
	e.feeds.feeds[chA] = Feed{Entries: append([]FeedEntry{e.entry(6, time.Minute), e.entry(7, 2*time.Minute), e.entry(8, 3*time.Minute)},
		e.feeds.feeds[chA].Entries...)}
	for range maxInfoFailures {
		e.svc.PollChannel(ctx, chA)
	}
	if got := queued(e); !slices.Contains(got, vid(8)+"/audio") || slices.Contains(got, vid(6)+"/audio") {
		t.Fatalf("the other new episode is queued: %v", got)
	}
	var kind string
	var fails int
	e.db.QueryRow(`SELECT kind, info_failures FROM episodes WHERE video_id=?`, vid(6)).Scan(&kind, &fails)
	if kind != "unavailable" || fails != maxInfoFailures {
		t.Fatalf("after %d failed reads: kind %q failures %d", maxInfoFailures, kind, fails)
	}
	e.yt.infoCalls = nil
	e.svc.PollChannel(ctx, chA)
	if slices.Contains(e.yt.infoCalls, vid(6)) {
		t.Fatal("skipped once it failed too often")
	}
	if e.count(`SELECT poll_failures FROM channels`) != 0 {
		t.Fatal("a failing video never backs the channel off")
	}
}

// A detail read that recovers before maxInfoFailures resets its count.
func TestDetailReadRecovers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.follow(e.user("anna"), chA)
	e.svc.InitialBackfill = 0
	e.clock.Add(time.Hour)
	e.yt.infoErr[vid(1)] = errors.New("yt-dlp: exit status 1: ERROR: something odd")
	e.yt.infos[vid(2)] = ytdlp.Info{ID: vid(2), LiveStatus: "is_live", Availability: "public"} // read fine every poll
	e.feeds.feeds[chA] = Feed{Entries: []FeedEntry{e.entry(1, time.Minute), e.entry(2, 2*time.Minute)}}
	e.svc.PollChannel(ctx, chA)
	if e.count(`SELECT info_failures FROM episodes WHERE video_id=?`, vid(1)) != 1 || len(queued(e)) != 0 {
		t.Fatal("one failure counted, nothing queued")
	}
	delete(e.yt.infoErr, vid(1))
	e.svc.PollChannel(ctx, chA)
	if e.count(`SELECT info_failures FROM episodes WHERE video_id=?`, vid(1)) != 0 || !slices.Equal(queued(e), []string{vid(1) + "/audio"}) {
		t.Fatalf("recovered: %v", queued(e))
	}
}

// Feed text is stripped of control characters and a
// video id listed twice in one feed is stored once (its first entry wins).
func TestStoreFeedSanitizesAndDeduplicates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.follow(e.user("anna"), chA)
	e.svc.InitialBackfill = 0
	a := e.entry(1, time.Hour)
	a.Title = "Ep\x00isode\x1b[31m one\u0085"
	a.Description = "line 1\nline\t2\x07\r\x7f"
	dup := e.entry(1, 2*time.Hour)
	dup.Title = "Duplicate"
	e.feeds.feeds[chA] = Feed{Title: "Chan\x08nel\nName", Entries: []FeedEntry{a, dup}}
	e.exec(`UPDATE channels SET title='' WHERE id=?`, chA)
	if err := e.svc.PollChannel(ctx, chA); err != nil {
		t.Fatal(err)
	}
	var title, desc string
	var published int64
	e.db.QueryRow(`SELECT title, description, published_at FROM episodes WHERE video_id=?`, vid(1)).Scan(&title, &desc, &published)
	if title != "Episode[31m one" || desc != "line 1\nline\t2" || published != a.Published.Unix() {
		t.Fatalf("title %q desc %q published %d", title, desc, published)
	}
	var ch string
	e.db.QueryRow(`SELECT title FROM channels WHERE id=?`, chA).Scan(&ch)
	if ch != "Channel Name" {
		t.Fatalf("channel title %q", ch)
	}
}

// detailState is every episode's "kind/info_failures", newest first.
func detailState(e *env) []string {
	e.t.Helper()
	rows, err := e.db.Query(`SELECT kind||'/'||info_failures FROM episodes ORDER BY published_at DESC`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var x string
		rows.Scan(&x)
		out = append(out, x)
	}
	return out
}

// Timeouts are push-back, never strikes.
func TestDetailReadTimeoutsArePushBack(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.follow(e.user("anna"), chA)
	for i := 1; i <= 4; i++ {
		e.yt.infoErr[vid(i)] = fmt.Errorf("channels: yt-dlp timed out: %w", context.DeadlineExceeded)
	}
	e.feeds.feeds[chA] = Feed{Entries: []FeedEntry{e.entry(1, time.Hour), e.entry(2, 2*time.Hour), e.entry(3, 3*time.Hour), e.entry(4, 4*time.Hour)}}
	for range maxInfoFailures + 1 {
		e.yt.infoCalls = nil
		if err := e.svc.PollChannel(ctx, chA); err != nil {
			t.Fatal(err)
		}
		if len(e.yt.infoCalls) != 1 {
			t.Fatalf("a timeout stops the poll's reads: %v", e.yt.infoCalls)
		}
	}
	if got := detailState(e); !slices.Equal(got, []string{"unknown/0", "unknown/0", "unknown/0", "unknown/0"}) {
		t.Fatalf("nothing marked: %v", got)
	}
	if e.count(`SELECT backfill_pending FROM channel_follows`) != 1 || len(queued(e)) != 0 {
		t.Fatal("backfill still pending")
	}
}

// Three generic failures in a row with no success are the
// environment, not the videos: the reads stop and nobody gets a strike.
func TestGenericFailureStreakStopsReadsWithoutStrikes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.follow(e.user("anna"), chA)
	var entries []FeedEntry
	for i := 1; i <= 5; i++ {
		e.yt.infoErr[vid(i)] = errors.New("yt-dlp: exit status 1: ERROR: [youtube] x: something odd")
		entries = append(entries, e.entry(i, time.Duration(i)*time.Hour))
	}
	e.feeds.feeds[chA] = Feed{Entries: entries}
	for range maxInfoFailures + 1 {
		e.yt.infoCalls = nil
		e.svc.PollChannel(ctx, chA)
		if len(e.yt.infoCalls) != 3 {
			t.Fatalf("three in a row stop the reads: %v", e.yt.infoCalls)
		}
	}
	if got := detailState(e); !slices.Equal(got, []string{"unknown/0", "unknown/0", "unknown/0", "unknown/0", "unknown/0"}) {
		t.Fatalf("no strikes: %v", got)
	}
	if e.count(`SELECT backfill_pending FROM channel_follows`) != 1 {
		t.Fatal("backfill still pending")
	}
}

// 429 and "try again later" during a detail read stop the
// reads; what was read stays, nothing else is marked, backfill waits.
func TestRateLimitDuringDetailReads(t *testing.T) {
	for _, msg := range []string{
		"yt-dlp: exit status 1: ERROR: Unable to download API page: HTTP Error 429: Too Many Requests",
		"yt-dlp: exit status 1: ERROR: [youtube] x: Video unavailable. This content isn't available, try again later.",
	} {
		e := newEnv(t)
		e.follow(e.user("anna"), chA)
		e.yt.infoErr[vid(2)] = errors.New(msg)
		e.feeds.feeds[chA] = Feed{Entries: []FeedEntry{e.entry(1, time.Hour), e.entry(2, 2*time.Hour), e.entry(3, 3*time.Hour), e.entry(4, 4*time.Hour)}}
		if err := e.svc.PollChannel(context.Background(), chA); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(e.yt.infoCalls, []string{vid(1), vid(2)}) {
			t.Fatalf("%s: reads %v", msg, e.yt.infoCalls)
		}
		if got := detailState(e); !slices.Equal(got, []string{"video/0", "unknown/0", "unknown/0", "unknown/0"}) {
			t.Fatalf("%s: %v", msg, got)
		}
		if e.count(`SELECT backfill_pending FROM channel_follows`) != 1 || len(queued(e)) != 0 {
			t.Fatalf("%s: backfill waits", msg)
		}
	}
}

// low turns its own timeout into context.DeadlineExceeded
// (yt-dlp killed by the deadline reports only "signal: killed").
func TestLowWrapsItsTimeout(t *testing.T) {
	e := newEnv(t)
	defer func(d time.Duration) { ytTimeout = d }(ytTimeout)
	ytTimeout = 10 * time.Millisecond
	err := e.svc.low(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		return errors.New("yt-dlp: signal: killed")
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("%v", err)
	}
	if err := e.svc.low(context.Background(), func(context.Context) error { return errors.New("boom") }); errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("other errors pass through")
	}
}

// expiredByUnfollow: a test account follows chA, its newest three are
// downloaded, it unfollows and the sweep expires them (the production state
// behind Alice's empty Latest). The clock then moves on an hour.
func expiredByUnfollow(t *testing.T, e *env) {
	t.Helper()
	ctx := context.Background()
	tester := e.user("tester")
	e.follow(tester, chA)
	e.feeds.feeds[chA] = Feed{Title: "A", Entries: []FeedEntry{
		e.entry(1, 20*24*time.Hour), e.entry(2, 22*24*time.Hour), e.entry(3, 25*24*time.Hour), e.entry(4, 50*24*time.Hour)}}
	if err := e.svc.PollChannel(ctx, chA); err != nil {
		t.Fatal(err)
	}
	for range 5 {
		e.svc.workStep(ctx)
	}
	if err := e.svc.Unfollow(ctx, tester, chA); err != nil {
		t.Fatal(err)
	}
	if n, err := e.svc.Sweep(ctx); err != nil || n != 3 {
		t.Fatalf("sweep after the unfollow expired %d (%v), want 3", n, err)
	}
	e.clock.Add(time.Hour)
}

// Production bug: episodes another follower downloaded and retention then
// expired are fetched again for a new follower's backfill, with their
// retention clock restarted, and show in the new follower's Latest.
func TestNewFollowerRequeuesExpiredBackfillPicks(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	expiredByUnfollow(t, e)
	e.exec(`UPDATE episode_files SET attempts=2, timeouts=1, transients=3, error='old', next_attempt_at=99 WHERE video_id=?`, vid(2))
	alice := e.user("alice")
	e.follow(alice, chA)
	if err := e.svc.PollChannel(ctx, chA); err != nil {
		t.Fatal(err)
	}
	if got := queued(e); !slices.Equal(got, []string{vid(1) + "/audio", vid(2) + "/audio", vid(3) + "/audio"}) {
		t.Fatalf("queued after the new follow: %v", got)
	}
	if e.count(`SELECT COUNT(*) FROM episode_files WHERE status='queued' AND attempts=0 AND timeouts=0 AND transients=0
		AND error='' AND next_attempt_at=0 AND path='' AND bytes=0 AND created_at=?`, e.clock.Now().Unix()) != 3 {
		t.Fatal("a re-queued file starts over: no attempts, no error, no backoff, created now")
	}
	for range 5 {
		e.svc.workStep(ctx)
	}
	for i := 1; i <= 3; i++ {
		if got := e.fileStatus(vid(i), "audio"); got != "done" {
			t.Fatalf("%s: %q, want done", vid(i), got)
		}
	}
	if n, err := e.svc.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("sweep right after the re-download expired %d (%v): the retention clock restarts", n, err)
	}
	eps, err := e.svc.Latest(ctx, alice, 0, 50)
	if err != nil || len(eps) != 3 {
		t.Fatalf("Latest: %d episodes (%v), want 3", len(eps), err)
	}
	// keep_days (10) later they go for good: Alice's own retention is never undone.
	e.clock.Add(11 * 24 * time.Hour)
	if n, _ := e.svc.Sweep(ctx); n != 3 {
		t.Fatalf("expired %d after keep_days, want 3", n)
	}
	e.clock.Add(time.Hour)
	if err := e.svc.PollChannel(ctx, chA); err != nil {
		t.Fatal(err)
	}
	if got := queued(e); len(got) != 0 {
		t.Fatalf("what the follower's own retention expired is not fetched again: %v", got)
	}
}

// After the deploy, Alice's follow already ran its backfill (pending 0)
// against the expired rows. The next poll heals it: none of its picks is on
// disk or on its way, and they expired before he followed.
func TestCompletedBackfillWithExpiredPicksHealsOnNextPoll(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	expiredByUnfollow(t, e)
	e.exec(`UPDATE episode_files SET attempts=3, status='expired', error='HTTP Error 404' WHERE video_id=?`, vid(3)) // a genuine failure, then expired
	alice := e.user("alice")
	e.follow(alice, chA)
	e.exec(`UPDATE channel_follows SET backfill_pending=0 WHERE user_id=?`, alice) // what the old code left behind
	e.clock.Add(time.Hour)
	if err := e.svc.PollChannel(ctx, chA); err != nil {
		t.Fatal(err)
	}
	if got := queued(e); !slices.Equal(got, []string{vid(1) + "/audio", vid(2) + "/audio"}) {
		t.Fatalf("queued after the healing poll: %v (a failed-for-good pick stays expired)", got)
	}
	if err := e.svc.PollChannel(ctx, chA); err != nil {
		t.Fatal(err)
	}
	if e.fileStatus(vid(3), "audio") != "expired" {
		t.Fatal("a genuinely failed file is never re-queued")
	}
}

// Healing looks only at the kinds the follower wants: an audio follower is
// not backfilled again for expired video files it would never fetch.
func TestHealIgnoresKindsTheFollowerDoesNotWant(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	expiredByUnfollow(t, e)
	e.exec(`UPDATE episode_files SET kind='video' WHERE kind='audio'`) // only expired video files are left
	alice := e.user("alice")
	e.follow(alice, chA)
	e.exec(`UPDATE channel_follows SET backfill_pending=0 WHERE user_id=?`, alice)
	var healed bool
	if err := e.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM channel_follows f WHERE f.user_id=? AND (`+healBackfill+`))`,
		alice, 3, 3).Scan(&healed); err != nil {
		t.Fatal(err)
	}
	if healed {
		t.Fatal("an audio follower is healed for video files it never wants")
	}
	e.exec(`UPDATE channel_follows SET media='video' WHERE user_id=?`, alice)
	e.db.QueryRow(`SELECT EXISTS (SELECT 1 FROM channel_follows f WHERE f.user_id=? AND (`+healBackfill+`))`, alice, 3, 3).Scan(&healed)
	if !healed {
		t.Fatal("a video follower is healed for expired video files")
	}
	if err := e.svc.PollChannel(ctx, chA); err != nil {
		t.Fatal(err)
	}
	if got := queued(e); len(got) != 6 {
		t.Fatalf("queued %v, want audio and video of the three picks", got)
	}
}
