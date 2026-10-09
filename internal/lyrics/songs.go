package lyrics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"
)

// SearchTerm is the free-text search every unofficial provider sends.
func SearchTerm(q Query) string { return strings.TrimSpace(q.Title + " " + q.Artist) }

// GetJSON is getJSON for other packages (the artwork providers).
func GetJSON(ctx context.Context, doer HTTPDoer, method, url string, body io.Reader, headers map[string]string, v any) error {
	return getJSON(ctx, doer, method, url, body, headers, v)
}

// Song is one NetEase / QQ Music search hit, shared by the lyrics and cover lookups.
type Song struct {
	ID, Title, Artist string // Artist: every singer, joined with ", "
	DurationS         int
	Album             string
	AlbumPic          string // NetEase al.picUrl ("" when absent)
	AlbumMID          string // QQ album.mid ("" when absent)
}

// Songs is the cloudsearch call (see NetEase); a "code" other than 200 is an error.
func (n *NetEase) Songs(ctx context.Context, term string) ([]Song, error) {
	v := url.Values{"s": {term}, "type": {"1"}, "limit": {"5"}, "offset": {"0"}}
	var sr neteaseSearch
	if err := getJSON(ctx, n.HTTP, "GET", n.base()+"/api/cloudsearch/pc?"+v.Encode(), nil, neteaseHeaders, &sr); err != nil {
		return nil, err
	}
	if sr.Code != 200 {
		return nil, fmt.Errorf("API code %d", sr.Code)
	}
	out := make([]Song, 0, len(sr.Result.Songs))
	for _, s := range sr.Result.Songs {
		names := make([]string, len(s.Ar))
		for i, a := range s.Ar {
			names[i] = a.Name
		}
		out = append(out, Song{ID: strconv.FormatInt(s.ID, 10), Title: s.Name, Artist: strings.Join(names, ", "),
			DurationS: (s.Dt + 500) / 1000, Album: s.Al.Name, AlbumPic: s.Al.PicURL})
	}
	return out, nil
}

// Songs is the musicu.fcg search call (see QQ); a missing result list is an error.
func (p *QQ) Songs(ctx context.Context, term string) ([]Song, error) {
	body, err := json.Marshal(map[string]any{
		"comm": map[string]any{"ct": 19, "cv": 1859, "uin": "0"},
		"req": map[string]any{
			"method": "DoSearchForQQMusicDesktop",
			"module": "music.search.SearchCgiService",
			"param":  map[string]any{"grp": 1, "num_per_page": 5, "page_num": 1, "query": term, "search_type": 0},
		},
	})
	if err != nil {
		return nil, err
	}
	var sr qqSearch
	if err := getJSON(ctx, p.HTTP, "POST", p.searchURL(), bytes.NewReader(body),
		map[string]string{"Content-Type": "application/json", "Referer": "https://y.qq.com/"}, &sr); err != nil {
		return nil, err
	}
	if sr.Code != 0 || sr.Req.Code != 0 {
		return nil, fmt.Errorf("API code %d/%d", sr.Code, sr.Req.Code)
	}
	if sr.Req.Data.Body.Song.List == nil {
		return nil, fmt.Errorf("no result list in the answer")
	}
	out := make([]Song, 0, len(*sr.Req.Data.Body.Song.List))
	for _, s := range *sr.Req.Data.Body.Song.List {
		names := make([]string, len(s.Singer))
		for i, a := range s.Singer {
			names[i] = a.Name
		}
		out = append(out, Song{ID: s.Mid, Title: s.Name, Artist: strings.Join(names, ", "), DurationS: s.Interval,
			Album: s.Album.Name, AlbumMID: s.Album.Mid})
	}
	return out, nil
}
