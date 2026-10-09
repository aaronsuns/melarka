package lyrics

import (
	"context"
	"fmt"
	"html"
	"net/url"
	"strings"
)

// QQ asks QQ Music's unofficial web API (no key). Search:
//
//	POST {SearchURL} (https://u.y.qq.com/cgi-bin/musicu.fcg), JSON
//	{"comm":{"ct":19,"cv":1859,"uin":"0"},"req":{"method":"DoSearchForQQMusicDesktop",
//	"module":"music.search.SearchCgiService","param":{"grp":1,"num_per_page":5,
//	"page_num":1,"query":"<title artist>","search_type":0}}}
//	→ req.data.body.song.list[]  (the old c.y.qq.com/soso client_search_cp answers HTTP 500)
//
// Lyrics:
//
//	GET {LyricURL}?songmid=<mid>&format=json&nobase64=1&g_tk=5381
//	(LyricURL default https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg,
//	Referer https://y.qq.com/) → {"retcode":0,"lyric":"…"} with HTML entities
//	(&#58; …) in the LRC; retcode != 0 means no lyric.
//
// Fixtures in testdata/ are trimmed copies of answers recorded 2026-10-01
// for 甜蜜蜜 / 邓丽君.
type QQ struct {
	HTTP      HTTPDoer // default: a client with a 10 s timeout
	SearchURL string   // default "https://u.y.qq.com/cgi-bin/musicu.fcg"
	LyricURL  string   // default "https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg"
}

func (*QQ) Name() string { return "qq" }

func (p *QQ) searchURL() string {
	if p.SearchURL == "" {
		return "https://u.y.qq.com/cgi-bin/musicu.fcg"
	}
	return p.SearchURL
}

func (p *QQ) lyricURL() string {
	if p.LyricURL == "" {
		return "https://c.y.qq.com/lyric/fcgi-bin/fcg_query_lyric_new.fcg"
	}
	return p.LyricURL
}

type qqSong struct {
	Mid      string `json:"mid"`
	Name     string `json:"name"`
	Interval int    `json:"interval"` // s
	Singer   []struct {
		Name string `json:"name"`
	} `json:"singer"`
	Album struct {
		Mid  string `json:"mid"`
		Name string `json:"name"`
	} `json:"album"`
}

type qqSearch struct {
	Code int `json:"code"`
	Req  struct {
		Code int `json:"code"`
		Data struct {
			Body struct {
				Song struct {
					List *[]qqSong `json:"list"` // nil = absent: not a valid answer
				} `json:"song"`
			} `json:"body"`
		} `json:"data"`
	} `json:"req"`
}

func (p *QQ) Search(ctx context.Context, q Query) ([]Candidate, error) {
	if strings.TrimSpace(q.Title) == "" {
		return nil, nil
	}
	songs, err := p.Songs(ctx, SearchTerm(q))
	if err != nil {
		return nil, fmt.Errorf("qq search: %w", err)
	}
	var out []Candidate
	var firstErr error
	fetched := 0
	for _, s := range songs {
		if fetched == unofficialMaxFetch {
			break
		}
		c := Candidate{Source: "qq", ExternalID: s.ID, Title: s.Title, Artist: s.Artist, DurationS: s.DurationS}
		if !Match(q, c) {
			continue
		}
		fetched++
		v := url.Values{"songmid": {s.ID}, "format": {"json"}, "nobase64": {"1"}, "g_tk": {"5381"}}
		var lr struct {
			Retcode int    `json:"retcode"`
			Lyric   string `json:"lyric"`
		}
		if err := getJSON(ctx, p.HTTP, "GET", p.lyricURL()+"?"+v.Encode(), nil, map[string]string{"Referer": "https://y.qq.com/"}, &lr); err != nil {
			if e := failed(ctx, fmt.Errorf("qq lyric %s: %w", s.ID, err), &firstErr); e != nil {
				return nil, e
			}
			continue
		}
		text := html.UnescapeString(lr.Lyric)
		if lr.Retcode != 0 || strings.TrimSpace(text) == "" {
			continue
		}
		c.Text = text
		_, c.Synced = ParseLRC(text)
		out = append(out, c)
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}
