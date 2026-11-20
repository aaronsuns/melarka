package ytdlp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// spiServer stands in for Bilibili's visitor-id endpoint: every call hands
// out a new buvid3 ("b3-<n>"). fail makes it answer 500.
func spiServer(t *testing.T, fail bool) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			http.Error(w, "no", 500)
			return
		}
		i := n.Add(1)
		fmt.Fprintf(w, `{"code":0,"data":{"b_3":"b3-%d","b_4":"b4-%d"}}`, i, i)
	}))
	t.Cleanup(ts.Close)
	return ts, &n
}

// cookieArg returns the --cookies file of argv and what it held when yt-dlp ran.
func cookieArg(args []string) (path, content string) {
	i := slices.Index(args, "--cookies")
	if i < 0 {
		return "", ""
	}
	b, _ := os.ReadFile(args[i+1])
	return args[i+1], string(b)
}

func TestSearchSourceBilibiliSendsAVisitorCookie(t *testing.T) {
	ts, calls := spiServer(t, false)
	good := fixture(t, "bilisearch.jsonl")
	var seen []string
	fr := &fakeRunner{}
	fr.outFor = func(args []string) ([]byte, error) {
		_, c := cookieArg(args)
		seen = append(seen, c)
		return good, nil
	}
	cl := &Client{Runner: fr, Bili: &BiliVisitor{URL: ts.URL}}
	for range 2 {
		if _, _, err := cl.SearchSource(context.Background(), SourceBilibili, "x", 10); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 2 || !strings.Contains(seen[0], "\tbuvid3\tb3-1\n") || !strings.Contains(seen[0], "\tbuvid4\tb4-1\n") ||
		!strings.Contains(seen[0], "# Netscape HTTP Cookie File") || seen[1] != seen[0] {
		t.Fatalf("cookie files %q", seen)
	}
	if calls.Load() != 1 {
		t.Errorf("the visitor id is fetched once and reused, got %d fetches", calls.Load())
	}
	path, _ := cookieArg(fr.gotArgs)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("cookie file %s left behind", path)
	}
}

// A refused search drops the visitor id, and the next try asks for a new one.
func TestSearchSourceBilibiliNewVisitorAfter412(t *testing.T) {
	defer func(d time.Duration) { BilibiliRetryDelay = d }(BilibiliRetryDelay)
	BilibiliRetryDelay = 0
	ts, calls := spiServer(t, false)
	good := fixture(t, "bilisearch.jsonl")
	var seen []string
	fr := &fakeRunner{}
	fr.outFor = func(args []string) ([]byte, error) {
		_, c := cookieArg(args)
		seen = append(seen, c)
		if len(seen) == 1 {
			return nil, errors.New("yt-dlp: exit status 1: ERROR: HTTP Error 412: Precondition Failed")
		}
		return good, nil
	}
	cl := &Client{Runner: fr, Bili: &BiliVisitor{URL: ts.URL}}
	if _, _, err := cl.SearchSource(context.Background(), SourceBilibili, "x", 10); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || !strings.Contains(seen[1], "b3-2") {
		t.Fatalf("%d fetches, cookie files %q", calls.Load(), seen)
	}
}

// When the visitor id can't be had, the search still runs, without --cookies.
func TestSearchSourceBilibiliVisitorDown(t *testing.T) {
	ts, _ := spiServer(t, true)
	fr := &fakeRunner{output: fixture(t, "bilisearch.jsonl")}
	cl := &Client{Runner: fr, Bili: &BiliVisitor{URL: ts.URL}}
	if _, _, err := cl.SearchSource(context.Background(), SourceBilibili, "x", 10); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(fr.gotArgs, "--cookies") {
		t.Errorf("no visitor id, yet --cookies in %v", fr.gotArgs)
	}
}
