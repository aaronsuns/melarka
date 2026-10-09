package channels

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestParseFeed(t *testing.T) {
	f, err := os.Open("testdata/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	feed, err := ParseFeed(f)
	if err != nil {
		t.Fatal(err)
	}
	if feed.Title != "刘翔的投资频道" || len(feed.Entries) != 3 {
		t.Fatalf("%q %d entries: %+v", feed.Title, len(feed.Entries), feed.Entries)
	}
	e := feed.Entries[0]
	if e.VideoID != "PncPZ-E1GjE" || e.Title != "引爆全球金融危機?爲了它,川普已偷偷抛棄美債" || e.Short ||
		!e.Published.Equal(time.Date(2026, 10, 3, 23, 41, 44, 0, time.UTC)) ||
		e.Description != "保证物超所值，管用的赚钱策略\nhttps://www.patreon.com/liuxiang" {
		t.Fatalf("entry 0 %+v", e)
	}
	if !feed.Entries[1].Short || feed.Entries[1].VideoID != "CEJXqm2eiJ0" {
		t.Fatalf("shorts link not detected: %+v", feed.Entries[1])
	}
	if feed.Entries[2].VideoID != "rvOZh8idOrU" || feed.Entries[2].Short {
		t.Fatalf("entry 2 %+v", feed.Entries[2])
	}
}

func TestParseFeedRejectsHTML(t *testing.T) {
	if _, err := ParseFeed(strings.NewReader("<!DOCTYPE html><html><body>Error 500</body></html>")); err == nil {
		t.Fatal("an HTML error page must not parse as an empty feed")
	}
}

func TestFeedClient(t *testing.T) {
	body, _ := os.ReadFile("testdata/feed.xml")
	var gotQuery, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotUA = r.URL.RawQuery, r.Header.Get("User-Agent")
		switch r.URL.Query().Get("channel_id") {
		case "UC0e5c4U67Vm6sAVK0vxN3Uw":
			w.Write(body)
		case "UCRABK12_6Ie2X549K9cXS0g":
			http.Error(w, "gone", 404)
		default:
			http.Error(w, "boom", 500)
		}
	}))
	defer srv.Close()
	c := &FeedClient{BaseURL: srv.URL + "/feeds/videos.xml"}
	ctx := context.Background()
	feed, err := c.Fetch(ctx, "UC0e5c4U67Vm6sAVK0vxN3Uw")
	if err != nil || len(feed.Entries) != 3 {
		t.Fatalf("%v %d", err, len(feed.Entries))
	}
	if gotQuery != "channel_id=UC0e5c4U67Vm6sAVK0vxN3Uw" || !strings.HasPrefix(gotUA, "Melarka/") {
		t.Fatalf("query %q ua %q", gotQuery, gotUA)
	}
	if _, err := c.Fetch(ctx, "UCRABK12_6Ie2X549K9cXS0g"); !errors.Is(err, ErrFeedNotFound) {
		t.Fatalf("404: %v", err)
	}
	if _, err := c.Fetch(ctx, "UCxxxxxxxxxxxxxxxxxxxxxx"); err == nil || errors.Is(err, ErrFeedNotFound) {
		t.Fatalf("500: %v", err)
	}
	gotQuery = ""
	if _, err := c.Fetch(ctx, "../../etc/passwd"); err == nil || gotQuery != "" {
		t.Fatalf("a bad id must never reach the network: %v %q", err, gotQuery)
	}
}
