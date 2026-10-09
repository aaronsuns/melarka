package lyrics

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// kugouMaxFetch: each Kugou hit costs two extra requests.
const kugouMaxFetch = 2

// Kugou asks Kugou's unofficial web API (no key) for LRC (never KRC):
//
//	GET {SearchURL}?format=json&keyword=<title artist>&page=1&pagesize=5
//	    (default http://mobilecdn.kugou.com/api/v3/search/song) → data.info[]
//	GET {CandidatesURL}?ver=1&man=yes&client=mobi&keyword=&duration=<ms>&hash=<hash>
//	    (default https://krcs.kugou.com/search) → candidates[0].{id, accesskey}
//	GET {DownloadURL}?ver=1&client=pc&id=<id>&accesskey=<key>&fmt=lrc&charset=utf8
//	    (default https://lyrics.kugou.com/download) → {"status":200,"content":"<base64>"}
//
// The decoded content is a BOM-prefixed LRC. Fixtures in testdata/ are
// trimmed copies of answers recorded 2026-10-01 for 甜蜜蜜 / 邓丽君.
type Kugou struct {
	HTTP          HTTPDoer // default: a client with a 10 s timeout
	SearchURL     string
	CandidatesURL string
	DownloadURL   string
}

func (*Kugou) Name() string { return "kugou" }

func strOr(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

type kugouSearch struct {
	Status  int    `json:"status"`
	Errcode int    `json:"errcode"`
	Error   string `json:"error"`
	Data    struct {
		Info []struct {
			Hash       string `json:"hash"`
			SongName   string `json:"songname"`
			SingerName string `json:"singername"`
			Duration   int    `json:"duration"` // s
		} `json:"info"`
	} `json:"data"`
}

func (k *Kugou) Search(ctx context.Context, q Query) ([]Candidate, error) {
	if strings.TrimSpace(q.Title) == "" {
		return nil, nil
	}
	v := url.Values{"format": {"json"}, "keyword": {SearchTerm(q)}, "page": {"1"}, "pagesize": {"5"}}
	var sr kugouSearch
	if err := getJSON(ctx, k.HTTP, "GET", strOr(k.SearchURL, "http://mobilecdn.kugou.com/api/v3/search/song")+"?"+v.Encode(), nil, nil, &sr); err != nil {
		return nil, fmt.Errorf("kugou search: %w", err)
	}
	if sr.Status != 1 {
		return nil, fmt.Errorf("kugou search: API status %d errcode %d %s", sr.Status, sr.Errcode, sr.Error)
	}
	var out []Candidate
	var firstErr error
	fetched := 0
	for _, s := range sr.Data.Info {
		if fetched == kugouMaxFetch {
			break
		}
		c := Candidate{Source: "kugou", ExternalID: s.Hash, Title: s.SongName, Artist: s.SingerName, DurationS: s.Duration}
		if !Match(q, c) {
			continue
		}
		fetched++
		text, err := k.lyric(ctx, s.Hash, s.Duration)
		if err != nil {
			if e := failed(ctx, fmt.Errorf("kugou lyric %s: %w", s.Hash, err), &firstErr); e != nil {
				return nil, e
			}
			continue
		}
		if strings.TrimSpace(text) == "" {
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

// lyric runs the candidates → download steps for one hash; "" with a nil
// error means Kugou has no lyric for it.
func (k *Kugou) lyric(ctx context.Context, hash string, durationS int) (string, error) {
	cv := url.Values{"ver": {"1"}, "man": {"yes"}, "client": {"mobi"}, "keyword": {""},
		"duration": {strconv.Itoa(durationS * 1000)}, "hash": {hash}}
	var cr struct {
		Candidates []struct {
			ID        string `json:"id"`
			AccessKey string `json:"accesskey"`
		} `json:"candidates"`
	}
	if err := getJSON(ctx, k.HTTP, "GET", strOr(k.CandidatesURL, "https://krcs.kugou.com/search")+"?"+cv.Encode(), nil, nil, &cr); err != nil {
		return "", err
	}
	if len(cr.Candidates) == 0 {
		return "", nil
	}
	dv := url.Values{"ver": {"1"}, "client": {"pc"}, "id": {cr.Candidates[0].ID}, "accesskey": {cr.Candidates[0].AccessKey},
		"fmt": {"lrc"}, "charset": {"utf8"}}
	var dr struct {
		Status  int    `json:"status"`
		Content string `json:"content"`
	}
	if err := getJSON(ctx, k.HTTP, "GET", strOr(k.DownloadURL, "https://lyrics.kugou.com/download")+"?"+dv.Encode(), nil, nil, &dr); err != nil {
		return "", err
	}
	if dr.Status != 200 || dr.Content == "" {
		return "", nil
	}
	raw, err := base64.StdEncoding.DecodeString(dr.Content)
	if err != nil {
		return "", fmt.Errorf("bad lyric content: %w", err)
	}
	return strings.TrimPrefix(string(raw), "\xEF\xBB\xBF"), nil
}
