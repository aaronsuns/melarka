package ytdlp

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// MediaKind is what a channel episode or preview download fetches.
type MediaKind string

const (
	MediaAudio MediaKind = "audio" // m4a (AAC)
	MediaVideo MediaKind = "video" // mp4, H.264/AAC preferred, at most 720p
)

// episodeFormats: audio is extracted to m4a with the thumbnail written next
// to it (<stem>.jpg, the episode's cover); video is ≤720p, H.264 + AAC
// preferred (what iOS plays), merged into mp4.
var episodeFormats = map[MediaKind][]string{
	MediaAudio: {"-f", "bestaudio[ext=m4a]/bestaudio/best", "-x", "--audio-format", "m4a", "--write-thumbnail", "--convert-thumbnails", "jpg"},
	MediaVideo: {"-f", "bv*[height<=720][vcodec^=avc1]+ba[ext=m4a]/b[height<=720][ext=mp4]/bv*[height<=720]+ba/b[height<=720]", "--merge-output-format", "mp4"},
}

// DownloadEpisode downloads one channel episode's audio or video into
// destNoExt.<ext> and returns the final path.
func (c *Client) DownloadEpisode(ctx context.Context, v Video, destNoExt string, kind MediaKind, onProgress func(pct float64)) (string, error) {
	format, ok := episodeFormats[kind]
	if !ok {
		return "", fmt.Errorf("ytdlp: unknown media kind %q", kind)
	}
	return c.fetch(ctx, v, destNoExt, format, onProgress, nil)
}

// PreviewMeta is what yt-dlp reports just before a preview starts downloading.
type PreviewMeta struct {
	Size        int64 // exact bytes of the chosen format; 0 when yt-dlp doesn't know it (always 0 when Merged)
	ChannelID   string
	Channel     string
	Title       string
	DurationS   int
	Description string // first 5000 runes of the video's description
	// Merged: yt-dlp picked a video and an audio stream (a video preview
	// without a usable format 18), merged into the mp4 only at the end — the
	// file can be served once it is complete, not while it grows.
	Merged   bool
	formatID string
}

// previewFormats: audio is YouTube's m4a (itag 140), a single file a
// browser plays while it grows. Video is a merged ≤360p — avc1 video + m4a
// audio preferred, what iOS plays — served once ffmpeg has merged it
// (yt-dlp's merger writes the moov atom first: it adds -movflags +faststart
// to every ffmpeg output, so seeking works from the start). A 15-minute
// video takes about 7 s on sun. Format 18 (progressive 360p) is not asked
// for: since 2026-10 YouTube lists it only for its android_vr client and
// answers its URLs with 403 most of the time, and --check-formats did not
// reliably skip it. A preview whose format_id is still 18 streams while it
// grows (the progressive path stays). When no format exists the download
// fails with "Requested format is not available" (FormatUnavailable).
var previewFormats = map[MediaKind][]string{
	MediaAudio: {"-f", "bestaudio[ext=m4a]"},
	MediaVideo: {"-f", "134+140/(bv*[height<=360][vcodec^=avc1]+ba[ext=m4a])/(bv*[height<=360]+ba)",
		"--merge-output-format", "mp4"},
}

const maxDescriptionRunes = 5000

const previewMetaPrint = "before_dl:LARKMETA %(.{filesize,channel_id,channel,title,duration,description,format_id})j"

// previewProgress prints the download's exact total size (yt-dlp's
// total_bytes: the format's filesize, or else the HTTP length it learns at
// the first byte; "NA" while unknown) and its percent. A merged download
// reports each stream in turn (video, then audio), each 0–100.
const previewProgress = "download:LARKSIZE %(progress.total_bytes)s %(progress._percent_str)s"

// DownloadPreview downloads a preview into destNoExt.<ext>. A single-file
// format is served while it grows: no .part file, no post-processing of
// the media (--fixup never), the thumbnail written next to it. onMeta gets
// the announced size and the channel before the first byte; when the size
// was not known then (format 18 often has none), onMeta is called once
// more, with the exact size, as soon as the download learns it.
// filesize_approx is deliberately not used: a Content-Range built on an
// estimate would cut off (or overrun) the real file. A merged video
// (PreviewMeta.Merged, from the format_id yt-dlp picked: anything but a
// single format) never announces a size. onProgress gets each progress
// line's percent.
func (c *Client) DownloadPreview(ctx context.Context, v Video, destNoExt string, kind MediaKind, onMeta func(PreviewMeta), onProgress func(pct float64)) (string, error) {
	base, ok := previewFormats[kind]
	if !ok {
		return "", fmt.Errorf("ytdlp: unknown media kind %q", kind)
	}
	format := append(append([]string{}, base...), "--no-part", "--fixup", "never",
		"--write-thumbnail", "--convert-thumbnails", "jpg", "--print", previewMetaPrint)
	var meta PreviewMeta
	return c.fetchWith(ctx, v, destNoExt, format, previewProgress, nil, func(line string) {
		if rest, ok := strings.CutPrefix(line, "LARKSIZE "); ok {
			f := strings.Fields(rest)
			if len(f) >= 2 && onProgress != nil {
				if pct, ok := parsePercent(f[1]); ok {
					onProgress(pct)
				}
			}
			if len(f) == 0 || meta.Merged {
				return
			}
			n, err := strconv.ParseInt(f[0], 10, 64)
			if err == nil && n > 0 && meta.Size == 0 {
				meta.Size = n
				if onMeta != nil {
					onMeta(meta)
				}
			}
			return
		}
		m, ok := parsePreviewMeta(line)
		if !ok {
			return
		}
		if kind == MediaVideo && (m.formatID == "" || strings.Contains(m.formatID, "+")) {
			m.Merged, m.Size = true, 0
		}
		m.formatID = ""
		meta = m
		if onMeta != nil {
			onMeta(meta)
		}
	})
}

// parsePreviewMeta reads a "LARKMETA {json}" line.
func parsePreviewMeta(line string) (PreviewMeta, bool) {
	rest, ok := strings.CutPrefix(line, "LARKMETA ")
	if !ok {
		return PreviewMeta{}, false
	}
	var raw struct {
		Filesize    *float64 `json:"filesize"`
		ChannelID   *string  `json:"channel_id"`
		Channel     *string  `json:"channel"`
		Title       *string  `json:"title"`
		Duration    *float64 `json:"duration"`
		Description *string  `json:"description"`
		FormatID    *string  `json:"format_id"`
	}
	if json.Unmarshal([]byte(rest), &raw) != nil {
		return PreviewMeta{}, false
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	m := PreviewMeta{ChannelID: str(raw.ChannelID), Channel: str(raw.Channel), Title: str(raw.Title), formatID: str(raw.FormatID)}
	if r := []rune(str(raw.Description)); len(r) > maxDescriptionRunes {
		m.Description = string(r[:maxDescriptionRunes])
	} else {
		m.Description = string(r)
	}
	if raw.Filesize != nil {
		m.Size = int64(*raw.Filesize)
	}
	if raw.Duration != nil {
		m.DurationS = int(*raw.Duration)
	}
	return m, true
}

// DownloadPreviewHD downloads the ≤720p merged mp4 (episodeFormats[MediaVideo],
// the same file an audio+video channel episode gets) into destNoExt.mp4 with
// its thumbnail, reporting progress and the same meta as DownloadPreview.
// It is not servable while it grows (merge happens at the end).
func (c *Client) DownloadPreviewHD(ctx context.Context, v Video, destNoExt string, onProgress func(pct float64), onMeta func(PreviewMeta)) (string, error) {
	format := append(append([]string{}, episodeFormats[MediaVideo]...),
		"--write-thumbnail", "--convert-thumbnails", "jpg", "--print", previewMetaPrint)
	return c.fetchWith(ctx, v, destNoExt, format, progressTemplate, onProgress, func(line string) {
		if m, ok := parsePreviewMeta(line); ok && onMeta != nil {
			m.formatID = ""
			onMeta(m)
		}
	})
}

var formatUnavailableRe = regexp.MustCompile(`(?i)requested format is not available`)

// FormatUnavailable reports whether err says none of the requested formats
// exists for the video (for a video preview: no mp4 of 360p or lower).
func FormatUnavailable(err error) bool {
	return err != nil && formatUnavailableRe.MatchString(err.Error())
}
