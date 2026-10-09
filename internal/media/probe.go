// Package media reads audio file information via ffprobe.
package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// maxStderrInError caps how much of ffprobe's stderr we fold into the
// returned error: enough to say what's wrong (e.g. "Invalid data found when
// processing input"), not enough for a pathological file to blow up log
// lines or the broken_reason column.
const maxStderrInError = 300

type Info struct {
	DurationMS  int64
	Codec       string
	BitrateKbps int
	SampleRate  int
	Lossless    bool
	Title       string
	Artist      string
	Album       string
	AlbumArtist string
	Year        int
	TrackNo     int
	DiscNo      int
}

type Prober interface {
	Probe(ctx context.Context, path string) (Info, error)
}

type FFprobe struct{ Path string }

type probeOut struct {
	Streams []struct {
		Index       int               `json:"index"`
		CodecType   string            `json:"codec_type"`
		CodecName   string            `json:"codec_name"`
		SampleRate  string            `json:"sample_rate"`
		BitRate     string            `json:"bit_rate"`
		Tags        map[string]string `json:"tags"`
		Disposition struct {
			AttachedPic int `json:"attached_pic"`
		} `json:"disposition"`
	} `json:"streams"`
	Format struct {
		Duration string            `json:"duration"`
		BitRate  string            `json:"bit_rate"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

func (p FFprobe) probeJSON(ctx context.Context, path string) (probeOut, error) {
	out, err := exec.CommandContext(ctx, p.Path, "-v", "error", "-print_format", "json",
		"-show_format", "-show_streams", path).Output()
	if err != nil {
		return probeOut{}, fmt.Errorf("ffprobe: %w", withStderr(err))
	}
	var po probeOut
	if err := json.Unmarshal(out, &po); err != nil {
		return probeOut{}, fmt.Errorf("ffprobe json: %w", err)
	}
	return po, nil
}

// AttachedPicture returns the stream index of the file's embedded cover (a
// video stream with disposition attached_pic), if it has one.
func (p FFprobe) AttachedPicture(ctx context.Context, path string) (int, bool, error) {
	po, err := p.probeJSON(ctx, path)
	if err != nil {
		return 0, false, err
	}
	for _, s := range po.Streams {
		if s.CodecType == "video" && s.Disposition.AttachedPic == 1 {
			return s.Index, true, nil
		}
	}
	return 0, false, nil
}

// Tags returns the file's metadata tags — the format's merged over the first
// audio stream's (ogg/opus keep them on the stream) — with lowercased keys.
func (p FFprobe) Tags(ctx context.Context, path string) (map[string]string, error) {
	po, err := p.probeJSON(ctx, path)
	if err != nil {
		return nil, err
	}
	tags := map[string]string{}
	for _, s := range po.Streams {
		if s.CodecType != "audio" {
			continue
		}
		for k, v := range s.Tags {
			tags[strings.ToLower(k)] = v
		}
		break
	}
	for k, v := range po.Format.Tags {
		tags[strings.ToLower(k)] = v
	}
	return tags, nil
}

func (p FFprobe) Probe(ctx context.Context, path string) (Info, error) {
	po, err := p.probeJSON(ctx, path)
	if err != nil {
		return Info{}, err
	}
	var info Info
	tags := map[string]string{}
	found := false
	for _, s := range po.Streams {
		if s.CodecType != "audio" {
			continue
		}
		found = true
		info.Codec = s.CodecName
		info.SampleRate, _ = strconv.Atoi(s.SampleRate)
		if br, err := strconv.Atoi(s.BitRate); err == nil {
			info.BitrateKbps = br / 1000
		}
		for k, v := range s.Tags { // ogg/opus keep tags on the stream
			tags[strings.ToLower(k)] = v
		}
		break
	}
	if !found {
		return Info{}, fmt.Errorf("no audio stream")
	}
	for k, v := range po.Format.Tags {
		tags[strings.ToLower(k)] = v
	}
	if d, err := strconv.ParseFloat(po.Format.Duration, 64); err == nil {
		info.DurationMS = int64(d * 1000)
	}
	if info.DurationMS <= 0 {
		return Info{}, fmt.Errorf("unknown duration")
	}
	if info.BitrateKbps == 0 {
		if br, err := strconv.Atoi(po.Format.BitRate); err == nil {
			info.BitrateKbps = br / 1000
		}
	}
	info.Lossless = IsLosslessCodec(info.Codec)
	info.Title = strings.TrimSpace(tags["title"])
	info.Artist = strings.TrimSpace(tags["artist"])
	info.Album = strings.TrimSpace(tags["album"])
	info.AlbumArtist = strings.TrimSpace(first(tags["album_artist"], tags["albumartist"]))
	info.Year = leadingInt(first(tags["date"], tags["year"]))
	info.TrackNo = leadingInt(first(tags["track"], tags["tracknumber"]))
	info.DiscNo = leadingInt(first(tags["disc"], tags["discnumber"]))
	return info, nil
}

// withStderr appends *exec.ExitError's captured stderr (trimmed to
// maxStderrInError bytes) to err's message, so a probe failure's
// broken_reason says something useful ("Invalid data found when processing
// input") instead of just "exit status 1". exec.Cmd.Output populates
// ExitError.Stderr automatically since Probe never sets Cmd.Stderr itself.
func withStderr(err error) error {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return err
	}
	stderr := strings.TrimSpace(string(ee.Stderr))
	if stderr == "" {
		return err
	}
	if len(stderr) > maxStderrInError {
		stderr = stderr[:maxStderrInError]
	}
	return fmt.Errorf("%w: %s", err, stderr)
}

func IsLosslessCodec(codec string) bool {
	return codec == "flac" || codec == "alac" || codec == "wmalossless" || codec == "ape" ||
		codec == "wavpack" || strings.HasPrefix(codec, "pcm_")
}

func first(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// leadingInt parses "3/12" → 3 and "1987-05-01" → 1987.
func leadingInt(s string) int {
	s = strings.TrimSpace(s)
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	n, _ := strconv.Atoi(s[:end])
	return n
}
