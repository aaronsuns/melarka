package ytdlp

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestDownloadEpisodeArgv(t *testing.T) {
	for kind, format := range map[MediaKind][]string{
		MediaAudio: {"-f", "bestaudio[ext=m4a]/bestaudio/best", "-x", "--audio-format", "m4a", "--write-thumbnail", "--convert-thumbnails", "jpg"},
		MediaVideo: {"-f", "bv*[height<=720][vcodec^=avc1]+ba[ext=m4a]/b[height<=720][ext=mp4]/bv*[height<=720]+ba/b[height<=720]", "--merge-output-format", "mp4"},
	} {
		fr := &fakeRunner{lines: []string{"LARKPROG 42.0%", "/ch/UCx/abc.m4a"}}
		c := &Client{Runner: fr}
		var got []float64
		p, err := c.DownloadEpisode(context.Background(), Video{ID: "x1_0000000x", URL: "https://www.youtube.com/watch?v=x1_0000000x"},
			"/ch/UCx/x1_0000000x", kind, func(pct float64) { got = append(got, pct) })
		if err != nil || p != "/ch/UCx/abc.m4a" || !reflect.DeepEqual(got, []float64{42}) {
			t.Fatalf("%s: %q %v %v", kind, p, got, err)
		}
		want := append([]string{"--js-runtimes", "node", "--newline", "--no-playlist", "--max-downloads", "1", "--no-overwrites", "--no-warnings", "--progress"}, format...)
		want = append(want, "--progress-template", "download:LARKPROG %(progress._percent_str)s", "--print", "after_move:filepath",
			"-o", "/ch/UCx/x1_0000000x.%(ext)s", "--", "https://www.youtube.com/watch?v=x1_0000000x")
		if !reflect.DeepEqual(fr.gotArgs, want) {
			t.Errorf("%s argv\n%q\nwant\n%q", kind, fr.gotArgs, want)
		}
	}
	fr := &fakeRunner{}
	if _, err := (&Client{Runner: fr}).DownloadEpisode(context.Background(), Video{URL: "https://evil.example/x"}, "/x", MediaAudio, nil); err != ErrBadURL || fr.gotArgs != nil {
		t.Fatalf("%v %q", err, fr.gotArgs)
	}
	if _, err := (&Client{Runner: fr}).DownloadEpisode(context.Background(), Video{URL: "https://www.youtube.com/watch?v=x1_0000000x"}, "/x", "flac", nil); err == nil {
		t.Fatal("unknown kind")
	}
}

func TestDownloadPreview(t *testing.T) {
	fr := &fakeRunner{lines: []string{
		`LARKMETA {"filesize": 14239128, "channel_id": "UC0e5c4U67Vm6sAVK0vxN3Uw", "channel": "刘翔的投资频道", "title": "T", "duration": 880.4, "format_id": "140"}`,
		"LARKSIZE 14239128   3.0%",
		"/p/x1_0000000x.audio.m4a",
	}}
	var meta PreviewMeta
	var pcts []float64
	p, err := (&Client{Runner: fr}).DownloadPreview(context.Background(), Video{ID: "x1_0000000x", URL: "https://www.youtube.com/watch?v=x1_0000000x"},
		"/p/x1_0000000x.audio", MediaAudio, func(m PreviewMeta) { meta = m }, func(p float64) { pcts = append(pcts, p) })
	if err != nil || p != "/p/x1_0000000x.audio.m4a" {
		t.Fatalf("%q %v", p, err)
	}
	if meta != (PreviewMeta{Size: 14239128, ChannelID: "UC0e5c4U67Vm6sAVK0vxN3Uw", Channel: "刘翔的投资频道", Title: "T", DurationS: 880}) {
		t.Fatalf("%+v", meta)
	}
	if !reflect.DeepEqual(pcts, []float64{3}) {
		t.Fatalf("progress %v", pcts)
	}
	want := []string{"--js-runtimes", "node", "--newline", "--no-playlist", "--max-downloads", "1", "--no-overwrites", "--no-warnings", "--progress",
		"-f", "bestaudio[ext=m4a]", "--no-part", "--fixup", "never", "--write-thumbnail", "--convert-thumbnails", "jpg",
		"--print", "before_dl:LARKMETA %(.{filesize,channel_id,channel,title,duration,description,format_id})j",
		"--progress-template", "download:LARKSIZE %(progress.total_bytes)s %(progress._percent_str)s", "--print", "after_move:filepath",
		"-o", "/p/x1_0000000x.audio.%(ext)s", "--", "https://www.youtube.com/watch?v=x1_0000000x"}
	if !reflect.DeepEqual(fr.gotArgs, want) {
		t.Fatalf("argv\n%q\nwant\n%q", fr.gotArgs, want)
	}
}

// Video: a merged ≤360p (avc1 + m4a preferred, what iOS plays) as mp4,
// never format 18 (YouTube answers its android_vr URLs with 403).
func TestDownloadPreviewVideoArgv(t *testing.T) {
	fr := &fakeRunner{lines: []string{`LARKMETA {"filesize": null, "channel_id": null, "format_id": "18"}`, "/p/v.video.mp4"}}
	(&Client{Runner: fr}).DownloadPreview(context.Background(), Video{ID: "x1_0000000x", URL: "https://www.youtube.com/watch?v=x1_0000000x"},
		"/p/v.video", MediaVideo, nil, nil)
	want := []string{"-f", "134+140/(bv*[height<=360][vcodec^=avc1]+ba[ext=m4a])/(bv*[height<=360]+ba)",
		"--merge-output-format", "mp4", "--no-part", "--fixup", "never"}
	if !reflect.DeepEqual(fr.gotArgs[9:9+len(want)], want) {
		t.Fatalf("argv %q", fr.gotArgs)
	}
	// yt-dlp's merger already writes the moov atom first (it adds
	// -movflags +faststart to every ffmpeg output): no postprocessor args.
	if strings.Contains(strings.Join(fr.gotArgs, " "), "--postprocessor-args") {
		t.Fatalf("argv %q", fr.gotArgs)
	}
}

// Which path ran comes from the format yt-dlp picked (format_id in the
// meta line): a single file (18) is progressive and streams while it
// grows; "134+140" (or an unknown format) is merged: no size is announced
// (a size would be one stream's, and the merged file appears only at the
// end) and the progress lines are what is reported.
func TestDownloadPreviewDetectsTheMergedPath(t *testing.T) {
	for _, tc := range []struct {
		formatID string
		merged   bool
	}{{`"18"`, false}, {`"134+140"`, true}, {`"160+140"`, true}, {"null", true}} {
		fr := &fakeRunner{lines: []string{
			`LARKMETA {"filesize": 4000, "channel_id": "UC0e5c4U67Vm6sAVK0vxN3Uw", "title": "T", "format_id": ` + tc.formatID + `}`,
			"LARKSIZE 3000  50.0%", "LARKSIZE 3000 100.0%", "LARKSIZE 1000  40.0%", "/p/v.video.mp4",
		}}
		var metas []PreviewMeta
		var pcts []float64
		_, err := (&Client{Runner: fr}).DownloadPreview(context.Background(), Video{ID: "x1_0000000x", URL: "https://www.youtube.com/watch?v=x1_0000000x"},
			"/p/v.video", MediaVideo, func(m PreviewMeta) { metas = append(metas, m) }, func(p float64) { pcts = append(pcts, p) })
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(pcts, []float64{50, 100, 40}) {
			t.Errorf("%s: progress %v", tc.formatID, pcts)
		}
		if tc.merged {
			if len(metas) != 1 || !metas[0].Merged || metas[0].Size != 0 || metas[0].ChannelID != "UC0e5c4U67Vm6sAVK0vxN3Uw" {
				t.Errorf("%s: %+v, want one merged meta without a size", tc.formatID, metas)
			}
			continue
		}
		if len(metas) != 1 || metas[0].Merged || metas[0].Size != 4000 {
			t.Errorf("%s: %+v, want progressive with the announced size", tc.formatID, metas)
		}
	}
	// Audio is always one file, whatever the meta says.
	fr := &fakeRunner{lines: []string{`LARKMETA {"filesize": null}`, "LARKSIZE 900 1.0%", "/p/a.audio.m4a"}}
	var metas []PreviewMeta
	(&Client{Runner: fr}).DownloadPreview(context.Background(), Video{ID: "x1_0000000x", URL: "https://www.youtube.com/watch?v=x1_0000000x"},
		"/p/a.audio", MediaAudio, func(m PreviewMeta) { metas = append(metas, m) }, nil)
	if len(metas) != 2 || metas[0].Merged || metas[1].Size != 900 {
		t.Fatalf("audio %+v", metas)
	}
}

func TestFormatUnavailable(t *testing.T) {
	for msg, want := range map[string]bool{
		"yt-dlp: exit status 1: ERROR: [youtube] x1_0000000x: Requested format is not available. Use --list-formats for a list of available formats": true,
		"yt-dlp: exit status 1: ERROR: [youtube] x: Sign in to confirm you're not a bot":                                                             false,
		"yt-dlp: exit status 1: ERROR: [youtube] x: Video unavailable":                                                                               false,
	} {
		if got := FormatUnavailable(errors.New(msg)); got != want {
			t.Errorf("%q: %v", msg, got)
		}
	}
	if FormatUnavailable(nil) {
		t.Error("nil")
	}
}

// Format 18 often has no filesize: the exact size then comes from the
// download itself (yt-dlp's total_bytes, the HTTP length) at the first
// byte, and onMeta is called again once with it.
func TestDownloadPreviewSizeFromTheDownload(t *testing.T) {
	fr := &fakeRunner{lines: []string{
		`LARKMETA {"filesize": null, "channel_id": "UC0e5c4U67Vm6sAVK0vxN3Uw", "channel": "C", "title": "T", "duration": 60, "format_id": "18"}`,
		"LARKSIZE NA NA", "LARKSIZE 5000   0.0%", "LARKSIZE 5000  10.0%", "/p/v.video.mp4",
	}}
	var metas []PreviewMeta
	_, err := (&Client{Runner: fr}).DownloadPreview(context.Background(), Video{ID: "x1_0000000x", URL: "https://www.youtube.com/watch?v=x1_0000000x"},
		"/p/v.video", MediaVideo, func(m PreviewMeta) { metas = append(metas, m) }, nil)
	want := []PreviewMeta{{ChannelID: "UC0e5c4U67Vm6sAVK0vxN3Uw", Channel: "C", Title: "T", DurationS: 60},
		{Size: 5000, ChannelID: "UC0e5c4U67Vm6sAVK0vxN3Uw", Channel: "C", Title: "T", DurationS: 60}}
	if err != nil || !reflect.DeepEqual(metas, want) {
		t.Fatalf("%+v %v", metas, err)
	}
}

func TestDownloadPreviewHDArgsAndMeta(t *testing.T) {
	r := &fakeRunner{lines: []string{
		`LARKMETA {"filesize": null, "channel_id": "UC0e5c4U67Vm6sAVK0vxN3Uw", "channel": "刘翔的投资频道", "title": "标题", "duration": 880, "description": "第一行\n<b>第二行</b>"}`,
		"LARKPROG 12.5%", "LARKPROG 100.0%", "/p/rvOZh8idOrU.hd.mp4"}}
	c := &Client{Runner: r}
	var meta PreviewMeta
	var pcts []float64
	path, err := c.DownloadPreviewHD(context.Background(), Video{ID: "rvOZh8idOrU", URL: WatchURL("rvOZh8idOrU")}, "/p/rvOZh8idOrU.hd",
		func(p float64) { pcts = append(pcts, p) }, func(m PreviewMeta) { meta = m })
	if err != nil || path != "/p/rvOZh8idOrU.hd.mp4" {
		t.Fatalf("path %q err %v", path, err)
	}
	args := strings.Join(r.gotArgs, " ")
	for _, want := range []string{episodeFormats[MediaVideo][1], "--merge-output-format mp4", "--write-thumbnail", "before_dl:LARKMETA"} {
		if !strings.Contains(args, want) {
			t.Errorf("argv lacks %q: %s", want, args)
		}
	}
	if strings.Contains(args, "--no-part") || strings.Contains(args, "--fixup never") {
		t.Errorf("an HD download is merged: no --no-part / --fixup never: %s", args)
	}
	if meta.Description != "第一行\n<b>第二行</b>" || meta.DurationS != 880 || meta.ChannelID != "UC0e5c4U67Vm6sAVK0vxN3Uw" {
		t.Errorf("meta %+v", meta)
	}
	if len(pcts) == 0 || pcts[len(pcts)-1] != 100 {
		t.Errorf("progress %v", pcts)
	}
}

func TestPreviewMetaCarriesDescription(t *testing.T) {
	if !strings.Contains(previewMetaPrint, "description") {
		t.Fatal("the meta print must ask for the description")
	}
	m, ok := parsePreviewMeta(`LARKMETA {"description": "` + strings.Repeat("长", 6000) + `"}`)
	if !ok || utf8.RuneCountInString(m.Description) != 5000 {
		t.Fatalf("description not capped at 5000 runes: ok=%v len=%d", ok, utf8.RuneCountInString(m.Description))
	}
}
