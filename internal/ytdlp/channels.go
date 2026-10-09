package ytdlp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Channel is a YouTube channel as Lark shows it.
type Channel struct {
	ID          string `json:"id"`     // UC… (IsChannelID)
	Title       string `json:"title"`  // display name
	Handle      string `json:"handle"` // "@liu-xiang", or ""
	Avatar      string `json:"avatar"` // https URL, or ""
	Description string `json:"description"`
	Followers   int    `json:"followers"`
}

// ErrNoChannel: the link resolved, but to nothing with a channel id.
var ErrNoChannel = errors.New("ytdlp: no channel at that link")

var channelIDRe = regexp.MustCompile(`^UC[A-Za-z0-9_-]{22}$`)

// IsChannelID reports whether id has the shape of a YouTube channel id.
func IsChannelID(id string) bool { return channelIDRe.MatchString(id) }

// ChannelURL is the canonical page of channel id.
func ChannelURL(id string) string { return "https://www.youtube.com/channel/" + id }

// channelNameRe is a handle (after "@") or a legacy /c/ or /user/ name.
var channelNameRe = regexp.MustCompile(`^[\p{L}\p{N}._-]{1,100}$`)

// ChannelPageURL returns the canonical page of the channel rawURL names —
// /channel/UC…, /@handle, /c/<name> or /user/<name>, any tab (/videos,
// /shorts, /featured) dropped — or "" when rawURL names no channel. rawURL
// must already have passed ValidURL.
func ChannelPageURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	segs := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case len(segs) >= 2 && segs[0] == "channel" && IsChannelID(segs[1]):
		return ChannelURL(segs[1])
	case strings.HasPrefix(segs[0], "@") && channelNameRe.MatchString(segs[0][1:]):
		return "https://www.youtube.com/@" + url.PathEscape(segs[0][1:])
	case len(segs) >= 2 && (segs[0] == "c" || segs[0] == "user") && channelNameRe.MatchString(segs[1]):
		return "https://www.youtube.com/" + segs[0] + "/" + url.PathEscape(segs[1])
	}
	return ""
}

// channelFilter is YouTube's "Channels" search filter (sp, already
// percent-encoded once, like playlistFilter).
const channelFilter = "EgIQAg%253D%253D"

// SearchChannels lists up to 10 channels matching q.
func (c *Client) SearchChannels(ctx context.Context, q string) ([]Channel, error) {
	clean, err := CleanQuery(q)
	if err != nil {
		return nil, err
	}
	args := append(c.baseArgs(), "--flat-playlist", "-J", "--no-warnings", "--playlist-end", "10",
		"https://www.youtube.com/results?search_query="+url.QueryEscape(clean)+"&sp="+channelFilter)
	out, err := c.Runner.Output(ctx, args)
	if err != nil {
		return nil, err
	}
	top, _, err := parseTop(out)
	if err != nil {
		return nil, err
	}
	chans := []Channel{}
	if top.Type != "playlist" {
		return chans, nil
	}
	for _, e := range top.Entries {
		if e.IEKey != "YoutubeTab" || !IsChannelID(e.ID) {
			continue
		}
		chans = append(chans, channelOf(e, e.ID))
	}
	return chans, nil
}

// ResolveChannel turns a pasted link into its channel: a channel page
// (/channel/UC…, /@handle, /c/…, /user/…) is read directly; a video link is
// first read for its channel id. Anything else (a playlist, a search) is
// ErrBadURL, before yt-dlp runs.
func (c *Client) ResolveChannel(ctx context.Context, rawURL string) (Channel, error) {
	u, err := ValidURL(rawURL)
	if err != nil {
		return Channel{}, err
	}
	page := ChannelPageURL(u)
	if page == "" {
		id := VideoIDFromURL(u)
		if id == "" {
			return Channel{}, ErrBadURL
		}
		info, err := c.VideoInfo(ctx, id)
		if err != nil {
			return Channel{}, err
		}
		if !IsChannelID(info.ChannelID) {
			return Channel{}, ErrNoChannel
		}
		page = ChannelURL(info.ChannelID)
	}
	args := append(c.baseArgs(), "--flat-playlist", "-J", "--no-warnings", "--playlist-end", "1", page)
	out, err := c.Runner.Output(ctx, args)
	if err != nil {
		return Channel{}, err
	}
	top, _, err := parseTop(out)
	if err != nil {
		return Channel{}, err
	}
	if !IsChannelID(top.ChannelID) {
		return Channel{}, ErrNoChannel
	}
	return channelOf(top.ytEntry, top.ChannelID), nil
}

const maxChannelDescription = 1000 // runes

// channelOf builds a Channel from a search entry or a channel page's top level.
func channelOf(e ytEntry, id string) Channel {
	title := entryChannel(e)
	if title == "" {
		title = e.Title
	}
	ch := Channel{ID: id, Title: title, Avatar: avatarOf(e.Thumbnails), Followers: e.Followers,
		Description: truncateRunes(strings.TrimSpace(e.Description), maxChannelDescription)}
	if strings.HasPrefix(e.UploaderID, "@") {
		ch.Handle = e.UploaderID
	}
	return ch
}

// avatarOf picks a channel's avatar: the thumbnail yt-dlp labels
// "avatar_uncropped", else the largest square one (banners are wide).
// Protocol-relative URLs get https; anything not https is dropped.
func avatarOf(ts []thumbnail) string {
	best, bestW := "", 0
	for _, t := range ts {
		if t.ID == "avatar_uncropped" {
			best = t.URL
			break
		}
		if t.Width > 0 && t.Width == t.Height && t.Width > bestW {
			best, bestW = t.URL, t.Width
		}
	}
	if strings.HasPrefix(best, "//") {
		best = "https:" + best
	}
	if !strings.HasPrefix(best, "https://") {
		return ""
	}
	return best
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// Info is one video's details, read without downloading anything.
type Info struct {
	ID, Title, Channel, ChannelID string
	DurationS                     int
	// yt-dlp's own vocabularies: live_status not_live|is_live|is_upcoming|
	// was_live|post_live; availability public|unlisted|subscriber_only|
	// needs_auth|premium_only|private; media_type video|short|livestream.
	LiveStatus, Availability, MediaType string
	Timestamp                           int64 // upload time, unix seconds (0 unknown)
	Description                         string
}

// infoTemplate prints only the fields Lark needs as one JSON object (the
// full -J dump of a video is ~80 KB).
const infoTemplate = "%(.{id,title,channel,channel_id,duration,live_status,availability,media_type,timestamp,description})j"

// VideoInfo reads one video's details (duration, live state, availability,
// channel) without downloading it.
func (c *Client) VideoInfo(ctx context.Context, id string) (Info, error) {
	if !IsVideoID(id) {
		return Info{}, ErrBadURL
	}
	args := append(c.baseArgs(), "--no-warnings", "--no-playlist", "--skip-download",
		// A scheduled premiere has no formats yet: without this yt-dlp errors out instead of printing its metadata.
		"--ignore-no-formats-error", "-O", infoTemplate, WatchURL(id))
	out, err := c.Runner.Output(ctx, args)
	if err != nil {
		return Info{}, err
	}
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	var raw struct {
		ID           string  `json:"id"`
		Title        string  `json:"title"`
		Channel      string  `json:"channel"`
		ChannelID    string  `json:"channel_id"`
		Duration     float64 `json:"duration"`
		LiveStatus   string  `json:"live_status"`
		Availability string  `json:"availability"`
		MediaType    string  `json:"media_type"`
		Timestamp    float64 `json:"timestamp"`
		Description  string  `json:"description"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return Info{}, fmt.Errorf("ytdlp: parse info: %w", err)
	}
	if raw.ID != id {
		return Info{}, fmt.Errorf("ytdlp: info for %s answered %q", id, raw.ID)
	}
	return Info{ID: raw.ID, Title: raw.Title, Channel: raw.Channel, ChannelID: raw.ChannelID,
		DurationS: int(raw.Duration), LiveStatus: raw.LiveStatus, Availability: raw.Availability,
		MediaType: raw.MediaType, Timestamp: int64(raw.Timestamp), Description: raw.Description}, nil
}

// unavailableRe: failures retrying can never fix — the video is private,
// removed, members-only, paid, or age-gated (that needs a login Lark never
// has). A bot check ("Sign in to confirm you're not a bot") and YouTube's
// "try again later" are deliberately not here: they pass.
var unavailableRe = regexp.MustCompile(`(?i)(private video|video unavailable|has been removed|members-only|join this channel|requires payment|confirm your age|age-restricted|account associated with this video has been terminated)`)

var transientRe = regexp.MustCompile(`(?i)(try again later|not a bot|HTTP Error 429|too many requests)`)

// networkRe: yt-dlp's signatures of a network or server problem, not a
// problem with the video.
var networkRe = regexp.MustCompile(`(?i)(unable to download (webpage|api page)|urlopen error|timed out|connection (reset|refused)|temporary failure in name resolution|HTTP Error 5\d\d)`)

// PushBack reports whether err is YouTube pushing back (a bot check, "try
// again later", 429), a timeout (an error wrapping context.DeadlineExceeded)
// or a network/server error rather than a problem with one video:
// background work stops for now and marks nothing.
func PushBack(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	msg := err.Error()
	return transientRe.MatchString(msg) || networkRe.MatchString(msg)
}

// forbiddenRe: YouTube refusing one file's media with a 403, which comes
// and goes (its throttling) rather than meaning the video is gone.
var forbiddenRe = regexp.MustCompile(`HTTP Error 403`)

// Forbidden reports whether err is a download YouTube answered with HTTP
// 403: transient, and that file's alone. Callers check PushBack first.
func Forbidden(err error) bool {
	return err != nil && forbiddenRe.MatchString(err.Error())
}

// Unavailable reports whether err (from VideoInfo or a download) means the
// video can never be fetched without an account.
func Unavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return unavailableRe.MatchString(msg) && !transientRe.MatchString(msg)
}
