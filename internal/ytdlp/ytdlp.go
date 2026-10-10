// Package ytdlp is the only place in this codebase that ever execs yt-dlp.
// Every call goes through exec.CommandContext with an argv slice (never a
// shell string), and every URL handed to yt-dlp is checked against a strict
// host allow-list first (ValidURL). Keep it that way: yt-dlp accepts
// "--"-prefixed strings as flags, so an unvalidated user string reaching
// argv is a command-injection path even without a shell involved.
package ytdlp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// Runner runs yt-dlp with args. Output streams stdout line by line to onLine
// (may be nil) and returns the full stdout; stderr is captured into the
// error on failure.
type Runner interface {
	Output(ctx context.Context, args []string) ([]byte, error)
	Stream(ctx context.Context, args []string, onLine func(string)) error
}

// ExecRunner runs the real yt-dlp binary. Bin resolves the binary path on
// every call (rather than being fixed at construction) so a yt-dlp that gets
// replaced on disk, e.g. /data/bin/yt-dlp updated by an admin action, is
// picked up by the very next call without restarting the server.
type ExecRunner struct {
	Bin func() string
	// Nice runs yt-dlp through nice(1) at low CPU priority (background work
	// such as recommendations, so it never competes with playback).
	Nice bool
}

// argv is the program and arguments actually executed.
func (r ExecRunner) argv(args []string) []string {
	a := []string{r.Bin()}
	if r.Nice {
		a = append([]string{"nice", "-n", "10"}, a...)
	}
	return append(a, args...)
}

// maxStderrCapture bounds how much of a failing process's stderr is folded
// into the returned error: enough to explain the failure (e.g. "ERROR:
// Video unavailable"), not enough for a pathological run to blow up log
// lines.
const maxStderrCapture = 4096 // 4 KiB

// capBuffer is a bytes.Buffer that keeps only the last maxBytes written to
// it, discarding from the front as more comes in.
type capBuffer struct {
	buf      bytes.Buffer
	maxBytes int
}

func (c *capBuffer) Write(p []byte) (int, error) {
	n, _ := c.buf.Write(p)
	if extra := c.buf.Len() - c.maxBytes; extra > 0 {
		c.buf.Next(extra)
	}
	return n, nil
}

func (c *capBuffer) String() string { return c.buf.String() }

// killWaitDelay bounds how long Wait will wait for the child's stdio pipes
// to close after it has been killed. yt-dlp's PyInstaller build forks
// children of its own (e.g. ffmpeg); killing only the top process can leave
// a grandchild holding a pipe open, and without WaitDelay that would hang
// Wait forever even though the process we asked to stop is long dead.
const killWaitDelay = 5 * time.Second

// killProcessGroup sends SIGKILL to the process group led by pid (a
// negative-pid kill). If the group is already gone — the process (and
// anything it forked) had already exited on its own right as ctx was
// cancelled — the kernel reports that as ESRCH. os/exec's Cmd.Cancel
// contract requires that "already exited" case to be reported as
// os.ErrProcessDone specifically: only that sentinel is suppressed when
// combining Cancel's result with the process's real exit status, so a raw
// ESRCH would otherwise make Wait return an error even though yt-dlp
// completed successfully — losing a valid finalPath in Download.
func killProcessGroup(pid int) error {
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}

// newCmd builds an *exec.Cmd that runs in its own process group (Setpgid)
// so that on ctx cancellation Cmd.Cancel can kill the whole group — not just
// the immediate child — via a negative-pid kill. Without this, yt-dlp's
// PyInstaller-bundled process forks a real child process (not an exec'd
// replacement), and killing only the parent leaves that child running and
// Wait blocked on it.
func (r ExecRunner) newCmd(ctx context.Context, args []string) *exec.Cmd {
	argv := r.argv(args)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = killWaitDelay
	cmd.Cancel = func() error {
		return killProcessGroup(cmd.Process.Pid)
	}
	return cmd
}

func (r ExecRunner) Output(ctx context.Context, args []string) ([]byte, error) {
	cmd := r.newCmd(ctx, args)
	var stdout bytes.Buffer
	stderr := &capBuffer{maxBytes: maxStderrCapture}
	cmd.Stdout = &stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		return nil, wrapExecErr(err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func (r ExecRunner) Stream(ctx context.Context, args []string, onLine func(string)) error {
	cmd := r.newCmd(ctx, args)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &capBuffer{maxBytes: maxStderrCapture}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // 1 MiB max line
	for scanner.Scan() {
		if onLine != nil {
			onLine(scanner.Text())
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil {
		// A scanner error (e.g. ErrTooLong) stops us reading, but yt-dlp may
		// still be writing: drain the rest of stdout so it doesn't block on
		// a full pipe buffer and leave Wait hanging.
		io.Copy(io.Discard, stdout)
	}
	waitErr := cmd.Wait()
	if waitErr != nil {
		return wrapExecErr(waitErr, stderr.String())
	}
	return scanErr
}

func wrapExecErr(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return fmt.Errorf("yt-dlp: %w", err)
	}
	return fmt.Errorf("yt-dlp: %w: %s", err, stderr)
}

// Client drives Runner with yt-dlp's argv conventions and parses its output.
type Client struct {
	Runner Runner
	// CacheDir, when set, is passed to every search/resolve/download as
	// --cache-dir (yt-dlp caches YouTube's player JS and signature solutions
	// there). Unset, yt-dlp uses $XDG_CACHE_HOME, which the image points at
	// the data volume; this field makes non-Docker runs behave the same.
	CacheDir string
}

// baseArgs leads every search/resolve/download argv. YouTube's signature
// and n-parameter challenges need a JavaScript runtime; yt-dlp only enables
// deno by default, and the image ships node.
func (c *Client) baseArgs() []string {
	a := []string{"--js-runtimes", "node"}
	if c.CacheDir != "" {
		a = append(a, "--cache-dir", c.CacheDir)
	}
	return a
}

// Video is one search result, playlist entry, or resolved URL. Tagged
// snake_case to match the rest of the API's wire format (the web client's
// YTVideo type expects these exact keys) — without tags, encoding/json would
// fall back to the Go field names (ID, Title, ...) instead.
type Video struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Channel   string `json:"channel"`
	URL       string `json:"url"`
	Thumbnail string `json:"thumbnail"`
	DurationS int    `json:"duration_s"`
	// ChannelID is the uploader's channel (UC…) when yt-dlp reported one:
	// what "follow this channel" on a video row and channel discovery need.
	ChannelID string `json:"channel_id,omitempty"`
	// Live: YouTube reports it as live or upcoming (a stream, not a song).
	Live bool `json:"-"`
}

// ErrBadURL is returned by ValidURL for anything not on the allow-list.
var ErrBadURL = errors.New("ytdlp: invalid or disallowed URL")

// ErrBadQuery is returned by CleanQuery for an empty or oversized query.
var ErrBadQuery = errors.New("ytdlp: invalid search query")

// allowedHosts is the strict allow-list of hosts yt-dlp is ever pointed at.
var allowedHosts = map[string]bool{
	"youtube.com":       true,
	"www.youtube.com":   true,
	"m.youtube.com":     true,
	"music.youtube.com": true,
	"youtu.be":          true,
}

// ValidURL accepts an http or https URL on the YouTube host allow-list and
// returns it normalised: scheme forced to https, any userinfo (e.g.
// "user:pass@") dropped, and an explicit port rejected outright (the
// allow-list is only meaningful if it can't be bypassed by pointing at a
// different service on a YouTube-looking host via a non-standard port).
// Anything else, including an unparseable string or one starting with "-"
// (which yt-dlp/getopt would treat as a flag rather than a positional URL
// argument), is ErrBadURL.
func ValidURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "-") {
		return "", ErrBadURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", ErrBadURL
	}
	switch u.Scheme {
	case "http", "https":
		u.Scheme = "https"
	default:
		return "", ErrBadURL
	}
	if u.Host == "" || u.Port() != "" {
		return "", ErrBadURL
	}
	if !allowedHosts[strings.ToLower(u.Hostname())] {
		return "", ErrBadURL
	}
	u.User = nil
	return u.String(), nil
}

// CleanQuery trims q and rejects it if empty or over 100 runes.
func CleanQuery(q string) (string, error) {
	q = strings.TrimSpace(q)
	n := utf8.RuneCountInString(q)
	if n == 0 || n > 100 {
		return "", ErrBadQuery
	}
	return q, nil
}

// Search runs a ytsearch10 flat-playlist lookup for q.
func (c *Client) Search(ctx context.Context, q string) ([]Video, error) {
	clean, err := CleanQuery(q)
	if err != nil {
		return nil, err
	}
	args := append(c.baseArgs(), "--flat-playlist", "-J", "--no-warnings", "ytsearch10:"+clean)
	out, err := c.Runner.Output(ctx, args)
	if err != nil {
		return nil, err
	}
	return parseVideos(out)
}

// SearchTop runs a ytsearch<n> flat-playlist lookup for q (n clamped to 1..10).
func (c *Client) SearchTop(ctx context.Context, q string, n int) ([]Video, error) {
	clean, err := CleanQuery(q)
	if err != nil {
		return nil, err
	}
	n = max(1, min(n, 10))
	args := append(c.baseArgs(), "--flat-playlist", "-J", "--no-warnings", "ytsearch"+strconv.Itoa(n)+":"+clean)
	out, err := c.Runner.Output(ctx, args)
	if err != nil {
		return nil, err
	}
	return parseVideos(out)
}

// MaxSearchResults bounds SearchMore: "show more" never asks YouTube for
// more than this many videos for one query.
const MaxSearchResults = 50

// SearchMore runs a ytsearch<n> flat-playlist lookup for q (n clamped to
// 1..MaxSearchResults); more reports that YouTube filled the whole page and
// n is still under the cap, so a larger n may find further videos. Each
// call fetches the first n from the top (ytsearch has no offset), so callers
// grow n and drop the videos they already have.
func (c *Client) SearchMore(ctx context.Context, q string, n int) (videos []Video, more bool, err error) {
	clean, err := CleanQuery(q)
	if err != nil {
		return nil, false, err
	}
	n = max(1, min(n, MaxSearchResults))
	args := append(c.baseArgs(), "--flat-playlist", "-J", "--no-warnings", "ytsearch"+strconv.Itoa(n)+":"+clean)
	out, err := c.Runner.Output(ctx, args)
	if err != nil {
		return nil, false, err
	}
	top, videos, err := parseTop(out)
	if err != nil {
		return nil, false, err
	}
	return videos, n < MaxSearchResults && len(top.Entries) >= n, nil
}

// Mix lists up to max entries of YouTube's Mix for videoID
// (watch?v=<id>&list=RD<id>), expanded as a playlist. Thumbnails are always
// YouTube's hqdefault (flat entries carry signed, expiring variants).
func (c *Client) Mix(ctx context.Context, videoID string, max int) ([]Video, error) {
	if !IsVideoID(videoID) {
		return nil, ErrBadURL
	}
	args := append(c.baseArgs(), "--flat-playlist", "-J", "--no-warnings", "--playlist-end", strconv.Itoa(max),
		WatchURL(videoID)+"&list=RD"+videoID)
	out, err := c.Runner.Output(ctx, args)
	if err != nil {
		return nil, err
	}
	videos, err := parseVideos(out)
	if err != nil {
		return nil, err
	}
	if max >= 0 && len(videos) > max {
		videos = videos[:max]
	}
	for i := range videos {
		videos[i].Thumbnail = ThumbnailURL(videos[i].ID)
	}
	return videos, nil
}

// Resolve runs a flat-playlist lookup on url: a single video resolves to one
// Video, a playlist to up to max entries. A URL that names one video (see
// VideoIDFromURL) gets --no-playlist, so a song link copied while it played
// inside a playlist or mix (watch?v=X&list=PL…/RD…) queues that song alone;
// only a URL without a video id (/playlist?list=…, a channel) expands.
func (c *Client) Resolve(ctx context.Context, rawURL string, max int) ([]Video, error) {
	l, err := c.ResolveList(ctx, rawURL, max)
	return l.Videos, err
}

// ResolveList is Resolve plus the playlist's own metadata (id, title, channel
// and YouTube's full length); for a single video those fields stay zero.
func (c *Client) ResolveList(ctx context.Context, rawURL string, max int) (List, error) {
	u, err := ValidURL(rawURL)
	if err != nil {
		return List{}, err
	}
	args := append(c.baseArgs(), "--flat-playlist", "-J", "--no-warnings")
	if VideoIDFromURL(u) != "" {
		args = append(args, "--no-playlist")
	}
	args = append(args, "--playlist-end", strconv.Itoa(max), u)
	out, err := c.Runner.Output(ctx, args)
	if err != nil {
		return List{}, err
	}
	top, videos, err := parseTop(out)
	if err != nil {
		return List{}, err
	}
	if max >= 0 && len(videos) > max {
		videos = videos[:max]
	}
	l := List{Videos: videos}
	if top.Type == "playlist" {
		l.ID, l.Title, l.Channel, l.Count = top.ID, top.Title, entryChannel(top.ytEntry), top.PlaylistCount
	}
	return l, nil
}

// Download extracts audio for v into destNoExt + "." + <ext>, streaming
// progress (0..100) to onProgress as it goes, and returns the final path
// yt-dlp reports after moving the file into place.
func (c *Client) Download(ctx context.Context, v Video, destNoExt string, onProgress func(pct float64)) (string, error) {
	return c.fetch(ctx, v, destNoExt, []string{
		"-f", "bestaudio[ext=m4a]/bestaudio/best",
		"-x", "--audio-format", "m4a",
		"--embed-thumbnail", "--convert-thumbnails", "jpg",
		"--embed-metadata",
	}, onProgress, nil)
}

// fetch runs one single-video download into destNoExt.<ext>: format holds
// the what-and-how flags (-f, -x, …); every other flag is shared by all
// downloads. Lines that are neither progress nor the final path go to
// onLine (may be nil) — previews read their --print before_dl line there.
func (c *Client) fetch(ctx context.Context, v Video, destNoExt string, format []string, onProgress func(pct float64), onLine func(string)) (string, error) {
	return c.fetchWith(ctx, v, destNoExt, format, progressTemplate, onProgress, onLine)
}

// progressTemplate makes yt-dlp print LARKPROG <percent> lines (onProgress).
const progressTemplate = "download:LARKPROG %(progress._percent_str)s"

// fetchWith is fetch with another --progress-template (previews read the
// download's total size from theirs, through onLine).
func (c *Client) fetchWith(ctx context.Context, v Video, destNoExt string, format []string, progress string, onProgress func(pct float64), onLine func(string)) (string, error) {
	// Validated here too (defence in depth), even though callers are
	// expected to have gotten v from Search/Resolve already: this is the
	// point where the URL actually reaches argv.
	u, err := ValidURL(v.URL)
	if err != nil {
		return "", err
	}
	// --progress is required for LARKPROG lines to appear at all: with
	// only --print after_move:filepath, yt-dlp runs in an implicit quiet
	// mode that suppresses the progress template entirely. --max-downloads
	// 1 is a second guard (after --no-playlist) that one job can never
	// download more than one video into its file.
	args := append(c.baseArgs(), "--newline", "--no-playlist", "--max-downloads", "1", "--no-overwrites", "--no-warnings", "--progress")
	args = append(args, format...)
	args = append(args,
		"--progress-template", progress,
		"--print", "after_move:filepath",
		"-o", destNoExt+".%(ext)s",
		"--", u,
	)
	var finalPath string
	err = c.Runner.Stream(ctx, args, func(line string) {
		t := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(t, "LARKPROG"); ok {
			if pct, ok := parsePercent(rest); ok && onProgress != nil {
				onProgress(pct)
			}
			return
		}
		if t != "" && filepath.IsAbs(t) {
			finalPath = t
			return
		}
		if t != "" && onLine != nil {
			onLine(t)
		}
	})
	// yt-dlp exits 101 once --max-downloads is reached, which with a limit
	// of 1 is every successful download: with a final path reported, that
	// exit is success.
	if err != nil && !(finalPath != "" && exitCode(err) == maxDownloadsExit) {
		return "", err
	}
	if finalPath == "" {
		return "", fmt.Errorf("ytdlp: download: yt-dlp reported no final path")
	}
	return finalPath, nil
}

// maxDownloadsExit is yt-dlp's exit status when --max-downloads stopped it.
const maxDownloadsExit = 101

// exitCode is err's process exit status (an *exec.ExitError anywhere in its
// chain), or -1.
func exitCode(err error) int {
	var ec interface{ ExitCode() int }
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return -1
}

// Version runs yt-dlp --version and returns its trimmed stdout.
func (c *Client) Version(ctx context.Context) (string, error) {
	out, err := c.Runner.Output(ctx, []string{"--version"})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// parsePercent parses the value half of a "LARKPROG <value>" line (the
// LARKPROG prefix already stripped): tolerates extra spaces and a trailing
// "%", and reports ok=false (rather than erroring) for a non-numeric value
// like "NA", which yt-dlp prints before it knows the total size.
func parsePercent(rest string) (pct float64, ok bool) {
	rest = strings.TrimSpace(rest)
	rest = strings.TrimSuffix(rest, "%")
	v, err := strconv.ParseFloat(rest, 64)
	if err != nil {
		return 0, false
	}
	return v, true
}

// thumbnail mirrors one entry of yt-dlp's "thumbnails" array (id/width/height
// pick a channel's avatar out of its banners).
type thumbnail struct {
	URL    string `json:"url"`
	ID     string `json:"id"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// ytEntry mirrors one flat-playlist entry, or the whole top-level object
// when yt-dlp returns a single video (not a playlist).
type ytEntry struct {
	Type       string      `json:"_type"`
	IEKey      string      `json:"ie_key"`
	ID         string      `json:"id"`
	Title      string      `json:"title"`
	Channel    string      `json:"channel"`
	Uploader   string      `json:"uploader"`
	Duration   float64     `json:"duration"`
	URL        string      `json:"url"`
	WebpageURL string      `json:"webpage_url"`
	Thumbnail  string      `json:"thumbnail"`
	Thumbnails []thumbnail `json:"thumbnails"`
	LiveStatus string      `json:"live_status"`

	ChannelID   string `json:"channel_id"`
	UploaderID  string `json:"uploader_id"`
	Description string `json:"description"`
	Followers   int    `json:"channel_follower_count"`
}

// ytTop is the top-level shape yt-dlp -J prints: either {"_type":"playlist",
// "entries":[...]} or a single video object with the same fields as an
// entry.
type ytTop struct {
	Type    string    `json:"_type"`
	Entries []ytEntry `json:"entries"`
	// PlaylistCount is YouTube's full playlist length (flat-playlist output
	// carries it even when --playlist-end cut the entries short).
	PlaylistCount int `json:"playlist_count"`
	ytEntry
}

func parseVideos(data []byte) ([]Video, error) {
	_, videos, err := parseTop(data)
	return videos, err
}

func parseTop(data []byte) (ytTop, []Video, error) {
	var top ytTop
	if err := json.Unmarshal(data, &top); err != nil {
		return ytTop{}, nil, fmt.Errorf("ytdlp: parse json: %w", err)
	}
	entries := top.Entries
	if top.Type != "playlist" {
		entries = []ytEntry{top.ytEntry}
	}
	videos := make([]Video, 0, len(entries))
	for _, e := range entries {
		if v, ok := entryToVideo(e); ok {
			videos = append(videos, v)
		}
	}
	return top, videos, nil
}

func entryChannel(e ytEntry) string {
	if e.Channel != "" {
		return e.Channel
	}
	return e.Uploader
}

// channelIDOf is e's channel id when it has the UC… shape, else "".
func channelIDOf(e ytEntry) string {
	if IsChannelID(e.ChannelID) {
		return e.ChannelID
	}
	return ""
}

// entryToVideo keeps only real videos (an 11-char id: channel, playlist
// and tab entries have longer ones) and always gives them the canonical
// watch URL — never the entry's own url, which may be a /shorts/ page or, on
// a fully-resolved single video, the direct googlevideo media stream.
func entryToVideo(e ytEntry) (Video, bool) {
	if !IsVideoID(e.ID) || e.Title == "[Private video]" || e.Title == "[Deleted video]" {
		return Video{}, false
	}
	channel := entryChannel(e)
	u := WatchURL(e.ID)
	thumb := e.Thumbnail
	if n := len(e.Thumbnails); n > 0 {
		thumb = e.Thumbnails[n-1].URL
	}
	return Video{
		ID:        e.ID,
		Title:     e.Title,
		Channel:   channel,
		URL:       u,
		Thumbnail: thumb,
		DurationS: int(e.Duration),
		ChannelID: channelIDOf(e),
		Live:      e.LiveStatus == "is_live" || e.LiveStatus == "is_upcoming",
	}, true
}
