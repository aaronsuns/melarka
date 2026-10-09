package artwork

import (
	"context"
	"fmt"
	"strings"

	"github.com/aaronsuns/lark-server/internal/lyrics"
)

// NetEase takes album pictures from NetEase Cloud Music's unofficial
// cloudsearch answer (the lyrics provider's search): result.songs[].al.picUrl
// is http://p2.music.126.net/<hash>/<id>.jpg; over https with ?param=1000y1000
// it serves 1000 px. Fixture: testdata/netease_search.json (2026-10-01).
type NetEase struct{ Search *lyrics.NetEase }

func (*NetEase) Name() string { return "netease" }

func (n *NetEase) Find(ctx context.Context, q lyrics.Query) ([]Found, error) {
	if strings.TrimSpace(q.Title) == "" {
		return nil, nil
	}
	songs, err := n.Search.Songs(ctx, lyrics.SearchTerm(q))
	if err != nil {
		return nil, fmt.Errorf("netease search: %w", err)
	}
	var out []Found
	for _, s := range songs {
		if s.AlbumPic == "" {
			continue
		}
		u := "https://" + strings.TrimPrefix(strings.TrimPrefix(s.AlbumPic, "https://"), "http://")
		out = append(out, Found{Source: "netease", ExternalID: s.ID, Title: s.Title, Artist: s.Artist, DurationS: s.DurationS,
			URL: u + "?param=1000y1000", Stream: -1})
	}
	return out, nil
}

// QQ takes album pictures from QQ Music's unofficial search answer (the
// lyrics provider's search): album.mid (empty for some hits, which are
// skipped) names https://y.gtimg.cn/music/photo_new/T002R800x800M000<mid>.jpg,
// 800 px being QQ's largest. Fixture: testdata/qq_search.json (2026-10-01).
type QQ struct{ Search *lyrics.QQ }

func (*QQ) Name() string { return "qq" }

func (p *QQ) Find(ctx context.Context, q lyrics.Query) ([]Found, error) {
	if strings.TrimSpace(q.Title) == "" {
		return nil, nil
	}
	songs, err := p.Search.Songs(ctx, lyrics.SearchTerm(q))
	if err != nil {
		return nil, fmt.Errorf("qq search: %w", err)
	}
	var out []Found
	for _, s := range songs {
		if s.AlbumMID == "" {
			continue
		}
		out = append(out, Found{Source: "qq", ExternalID: s.ID, Title: s.Title, Artist: s.Artist, DurationS: s.DurationS,
			URL: "https://y.gtimg.cn/music/photo_new/T002R800x800M000" + s.AlbumMID + ".jpg", Stream: -1})
	}
	return out, nil
}
