package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MaxBilibiliResults bounds a Bilibili search. Unlike YouTube's, it is a full
// lookup (yt-dlp's flat Bilibili results carry no titles and numeric ids), so
// each result costs one page fetch: kept small.
const MaxBilibiliResults = 20

// ErrBilibiliThrottled: Bilibili answered HTTP 412, its anti-bot refusal.
// It passes after a while (PushBack is true for it).
var ErrBilibiliThrottled = errors.New("ytdlp: bilibili is throttling (HTTP 412)")

var bili412Re = regexp.MustCompile(`HTTP Error 412`)

// ClassifyBilibili maps a yt-dlp failure against Bilibili to
// ErrBilibiliThrottled when it is a 412; anything else is returned as is.
func ClassifyBilibili(err error) error {
	if err == nil || !bili412Re.MatchString(err.Error()) {
		return err
	}
	return fmt.Errorf("%w: %s", ErrBilibiliThrottled, LastLine(err.Error()))
}

// biliTemplate prints one Bilibili video as one JSON line.
const biliTemplate = "%(.{id,title,uploader,duration,thumbnail,view_count})j"

// biliListTemplate prints the list's own metadata once, after its entries.
const biliListTemplate = "playlist:LARKLIST %(.{id,title,uploader,playlist_count})j"

// biliArgs are the flags of every full Bilibili lookup: no download, and
// --no-playlist so a multi-part video gives part 1 only. The search, space
// and favourites extractors ignore --no-playlist (their entries still come).
func (c *Client) biliArgs() []string {
	return append(c.baseArgs(), "--no-warnings", "--skip-download", "--ignore-no-formats-error", "--no-playlist", "-O", biliTemplate)
}

// SearchSource searches src for q. YouTube is SearchMore; Bilibili runs
// bilisearch<n> (n clamped to 1..MaxBilibiliResults) and has no playlist
// search. more reports a full page under the cap.
func (c *Client) SearchSource(ctx context.Context, src Source, q string, n int) (videos []Video, more bool, err error) {
	if src != SourceBilibili {
		return c.SearchMore(ctx, q, n)
	}
	clean, err := CleanQuery(q)
	if err != nil {
		return nil, false, err
	}
	n = max(1, min(n, MaxBilibiliResults))
	out, err := c.Runner.Output(ctx, append(c.biliArgs(), "bilisearch"+strconv.Itoa(n)+":"+clean))
	if err != nil {
		return nil, false, ClassifyBilibili(err)
	}
	_, videos = parseBiliLines(out)
	return videos, len(videos) >= n && n < MaxBilibiliResults, nil
}

// biliPlayListRe: the play-list pages (/list/<id>?bvid=…) whose extractor
// returns just the playing video under --no-playlist.
var biliPlayListRe = regexp.MustCompile(`^https://(www\.)?bilibili\.com/(list|medialist/play)/`)

// resolveBilibili is ResolveList for an allow-listed Bilibili URL u.
func (c *Client) resolveBilibili(ctx context.Context, u string, max int) (List, error) {
	args := c.biliArgs()
	if VideoIDFromURL(u) == "" {
		args = append(args, "--print", biliListTemplate)
		if biliPlayListRe.MatchString(u) {
			args = withoutFlag(args, "--no-playlist")
		}
	}
	args = append(args, "--playlist-end", strconv.Itoa(max), u)
	out, err := c.Runner.Output(ctx, args)
	if err != nil {
		return List{}, ClassifyBilibili(err)
	}
	l, videos := parseBiliLines(out)
	if max >= 0 && len(videos) > max {
		videos = videos[:max]
	}
	l.Videos = videos
	return l, nil
}

// withoutFlag returns args without flag.
func withoutFlag(args []string, flag string) []string {
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a != flag {
			out = append(out, a)
		}
	}
	return out
}

// biliPartRe is the "_p<N>" suffix yt-dlp gives one part of a multi-part video.
var biliPartRe = regexp.MustCompile(`_p\d+$`)

// parseBiliLines reads biliTemplate lines (and an optional LARKLIST line).
// Lines that do not parse or have no Bilibili id are skipped.
func parseBiliLines(out []byte) (List, []Video) {
	var l List
	videos := []Video{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "LARKLIST "); ok {
			var m struct {
				ID       string `json:"id"`
				Title    string `json:"title"`
				Uploader string `json:"uploader"`
				Count    int    `json:"playlist_count"`
			}
			if json.Unmarshal([]byte(rest), &m) == nil {
				l.ID, l.Title, l.Channel, l.Count = m.ID, m.Title, m.Uploader, m.Count
			}
			continue
		}
		var e struct {
			ID        string  `json:"id"`
			Title     string  `json:"title"`
			Uploader  string  `json:"uploader"`
			Duration  float64 `json:"duration"`
			Thumbnail string  `json:"thumbnail"`
			Views     int64   `json:"view_count"`
		}
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		id := biliPartRe.ReplaceAllString(e.ID, "")
		if !IsBilibiliID(id) {
			continue
		}
		videos = append(videos, Video{ID: id, Title: e.Title, Channel: e.Uploader, URL: WatchURL(id),
			Thumbnail: e.Thumbnail, DurationS: int(e.Duration), Views: e.Views})
	}
	return l, videos
}
