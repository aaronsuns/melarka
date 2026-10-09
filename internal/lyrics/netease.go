package lyrics

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// unofficialMaxFetch caps how many pre-filtered hits have their lyrics fetched
// (Kugou uses 2: every hit costs it two extra requests).
const unofficialMaxFetch = 3

// failed ends a hit loop: a cancelled context aborts the whole search, any
// other per-hit failure is kept in *first and only reported when the search
// produced nothing.
func failed(ctx context.Context, err error, first *error) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%w", ctx.Err())
	}
	if *first == nil {
		*first = err
	}
	return nil
}

// NetEase asks NetEase Cloud Music's unofficial web API (no key). Search:
//
//	GET {BaseURL}/api/cloudsearch/pc?s=<title artist>&type=1&limit=5&offset=0
//	(Referer https://music.163.com/, User-Agent Mozilla/5.0)
//
// whose "code" must be 200: -462 is the "bind a phone" captcha wall that
// /api/search/pc answers with, and /api/search/get/web returns an encrypted
// string, so neither is used. Lyrics:
//
//	GET {BaseURL}/api/song/lyric?id=<id>&lv=1&tv=-1  → lrc.lyric
//
// Fixtures in testdata/ are trimmed copies of answers recorded 2026-10-01
// for 甜蜜蜜 / 邓丽君.
type NetEase struct {
	HTTP    HTTPDoer // default: a client with a 10 s timeout
	BaseURL string   // default "https://music.163.com"
}

func (*NetEase) Name() string { return "netease" }

func (n *NetEase) base() string {
	if n.BaseURL == "" {
		return "https://music.163.com"
	}
	return strings.TrimSuffix(n.BaseURL, "/")
}

var neteaseHeaders = map[string]string{"Referer": "https://music.163.com/", "User-Agent": "Mozilla/5.0"}

type neteaseSearch struct {
	Code   int `json:"code"`
	Result struct {
		Songs []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Dt   int    `json:"dt"` // ms
			Ar   []struct {
				Name string `json:"name"`
			} `json:"ar"`
			Al struct {
				Name   string `json:"name"`
				PicURL string `json:"picUrl"`
			} `json:"al"`
		} `json:"songs"`
	} `json:"result"`
}

type neteaseLyric struct {
	Code      int  `json:"code"`
	PureMusic bool `json:"pureMusic"`
	NoLyric   bool `json:"nolyric"`
	Lrc       struct {
		Lyric string `json:"lyric"`
	} `json:"lrc"`
}

func (n *NetEase) Search(ctx context.Context, q Query) ([]Candidate, error) {
	if strings.TrimSpace(q.Title) == "" {
		return nil, nil
	}
	songs, err := n.Songs(ctx, SearchTerm(q))
	if err != nil {
		return nil, fmt.Errorf("netease search: %w", err)
	}
	var out []Candidate
	var firstErr error
	fetched := 0
	for _, s := range songs {
		if fetched == unofficialMaxFetch {
			break
		}
		c := Candidate{Source: "netease", ExternalID: s.ID, Title: s.Title, Artist: s.Artist, DurationS: s.DurationS}
		if !Match(q, c) {
			continue
		}
		fetched++
		lv := url.Values{"id": {c.ExternalID}, "lv": {"1"}, "tv": {"-1"}}
		var lr neteaseLyric
		if err := getJSON(ctx, n.HTTP, "GET", n.base()+"/api/song/lyric?"+lv.Encode(), nil, neteaseHeaders, &lr); err != nil {
			if e := failed(ctx, fmt.Errorf("netease lyric %s: %w", c.ExternalID, err), &firstErr); e != nil {
				return nil, e
			}
			continue
		}
		if lr.Code != 200 { // a refusal (e.g. -462 captcha wall) is not "no lyrics"
			if e := failed(ctx, fmt.Errorf("netease lyric %s: API code %d", c.ExternalID, lr.Code), &firstErr); e != nil {
				return nil, e
			}
			continue
		}
		if lr.PureMusic || lr.NoLyric || strings.TrimSpace(lr.Lrc.Lyric) == "" {
			continue
		}
		c.Text = lr.Lrc.Lyric
		_, c.Synced = ParseLRC(c.Text)
		out = append(out, c)
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}
